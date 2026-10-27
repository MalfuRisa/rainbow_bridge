import http from 'node:http'
import { createHash } from 'node:crypto'
import { chromium } from 'playwright'

function pageHTML({ title, apiRoot, environment, pipeline }) {
  return `<!doctype html>
<html><head><meta charset="utf-8"><title>${title}</title>
<style>body{font:16px system-ui;margin:24px} #banner{display:block;margin-top:12px;border:4px solid #222}</style>
</head><body>
<h1>${title}</h1>
<p>scope: <strong id="scope"></strong></p>
<p>version: <strong id="version"></strong></p>
<p>marker: <strong id="marker"></strong></p>
<img id="banner" alt="banner">
<script>
const apiRoot = ${JSON.stringify(apiRoot)};
const environment = ${JSON.stringify(environment)};
const pipeline = ${JSON.stringify(pipeline)};
window.__runtimeBusiness = { ready:false };
(async () => {
  try {
    const response = await fetch(apiRoot + '/api/v1/runtime/release', {
      headers: { 'x-environment': environment, 'x-pipeline': pipeline },
      credentials: 'omit',
      cache: 'no-store',
    });
    const payload = await response.json();
    if (!response.ok) throw new Error('HTTP ' + response.status + ' ' + (payload.error_type || ''));
    const configs = payload.data.configs || [];
    const greeting = configs.find(item => item.alias === 'greeting');
    const banner = configs.find(item => item.alias === 'banner');
    if (!greeting || !banner) throw new Error('missing greeting/banner');
    document.getElementById('scope').textContent = payload.data.environment.environment_key + '/' + payload.data.environment.pipeline_key;
    document.getElementById('version').textContent = payload.data.version;
    document.getElementById('marker').textContent = greeting.content;
    const imageUrl = /^https?:/i.test(banner.content) ? banner.content : new URL(banner.content, apiRoot + '/').href;
    const imageResponse = await fetch(imageUrl, { credentials: 'omit', cache: 'no-store' });
    const bytes = new Uint8Array(await imageResponse.arrayBuffer());
    const digest = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))]
      .map(value => value.toString(16).padStart(2, '0')).join('');
    const image = document.getElementById('banner');
    image.src = imageUrl;
    await new Promise((resolve, reject) => {
      image.onload = resolve;
      image.onerror = () => reject(new Error('image decode failed'));
    });
    window.__runtimeBusiness = {
      ready: true,
      status: response.status,
      scope: document.getElementById('scope').textContent,
      version: payload.data.version,
      marker: greeting.content,
      snapshot_id: payload.data.snapshot_id,
      selection_mode: payload.data.selection_mode,
      image_url: imageUrl,
      image_status: imageResponse.status,
      image_type: imageResponse.headers.get('content-type'),
      image_sha256: digest,
      image_length: bytes.length,
      naturalWidth: image.naturalWidth,
      naturalHeight: image.naturalHeight,
    };
  } catch (error) {
    window.__runtimeBusiness = { ready:false, error: String(error && error.message || error) };
  }
})();
</script>
</body></html>`
}

export async function startBusinessPage(port, config) {
  const html = pageHTML(config)
  const server = http.createServer((request, response) => {
    response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' })
    response.end(html)
  })
  await new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(port, '127.0.0.1', resolve)
  })
  return {
    origin: `http://127.0.0.1:${port}`,
    async close() {
      await new Promise(resolve => server.close(resolve))
    },
  }
}

export async function runBrowserScenarios({ pages, expected }) {
  const browser = await chromium.launch({ headless: true })
  const results = []
  try {
    for (const pageInfo of pages) {
      const context = await browser.newContext()
      const page = await context.newPage()
      const consoleErrors = []
      page.on('console', message => {
        if (message.type() === 'error') consoleErrors.push(message.text())
      })
      page.on('pageerror', error => consoleErrors.push(String(error)))
      await page.goto(`${pageInfo.server.origin}/`, { waitUntil: 'domcontentloaded' })
      await page.waitForFunction(() => window.__runtimeBusiness?.ready === true ||
        Object.hasOwn(window.__runtimeBusiness ?? {}, 'error'), null, { timeout: 20000 })
      const runtime = await page.evaluate(() => window.__runtimeBusiness)
      const screenshot = await page.screenshot({ fullPage: true })
      const screenshotSha = createHash('sha256').update(screenshot).digest('hex')
      const want = expected[pageInfo.key]
      if (!runtime.ready) throw new Error(`browser ${pageInfo.key} failed: ${runtime.error}; console=${consoleErrors.join(' | ')}`)
      if (runtime.status !== 200 || runtime.marker !== want.marker || runtime.scope !== want.scope) {
        throw new Error(`browser ${pageInfo.key} content mismatch: ${JSON.stringify(runtime)}`)
      }
      if (runtime.image_sha256 !== want.imageSha || runtime.image_status !== 200 ||
        runtime.naturalWidth <= 0 || runtime.naturalHeight <= 0) {
        throw new Error(`browser ${pageInfo.key} image mismatch: ${JSON.stringify(runtime)}`)
      }
      results.push({ key: pageInfo.key, runtime, screenshot, screenshotSha, consoleErrors })
      await context.close()
    }
    if (results.length === 2 && results[0].screenshotSha === results[1].screenshotSha) {
      throw new Error('A/B screenshots are identical; images may not be distinct')
    }
    return results
  } finally {
    await browser.close()
  }
}