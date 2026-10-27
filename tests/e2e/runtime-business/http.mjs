import http from 'node:http'
import { randomUUID } from 'node:crypto'

let observer = null
let activeCase = null

export function setRequestObserver(fn) {
  observer = fn
}

export function setActiveCase(id) {
  activeCase = id
}

export function targetUrl(target, logicalPath) {
  if (/^https?:\/\//i.test(logicalPath)) return logicalPath
  const prefix = target.prefix && target.prefix !== '/' ? target.prefix.replace(/\/$/, '') : ''
  return `${target.origin}${prefix}${logicalPath.startsWith('/') ? logicalPath : `/${logicalPath}`}`
}

export async function request(target, logicalPath, init = {}) {
  const url = targetUrl(target, logicalPath)
  const parsed = new URL(url)
  const method = (init.method ?? 'GET').toUpperCase()
  const headers = { ...(init.headers ?? {}) }
  let body = init.body
  if (body !== undefined && !Buffer.isBuffer(body) && typeof body !== 'string') {
    body = JSON.stringify(body)
    headers['Content-Type'] ??= 'application/json'
  }
  if (body !== undefined && !headers['Content-Length']) {
    headers['Content-Length'] = Buffer.byteLength(body)
  }
  const started = Date.now()
  const response = await new Promise((resolve, reject) => {
    const requestHandle = http.request({
      protocol: parsed.protocol,
      hostname: parsed.hostname,
      port: parsed.port,
      path: parsed.pathname + parsed.search,
      method,
      headers,
    }, responseHandle => {
      const chunks = []
      responseHandle.on('data', chunk => chunks.push(chunk))
      responseHandle.on('end', () => resolve({
        status: responseHandle.statusCode ?? 0,
        headers: responseHandle.headers,
        bytes: Buffer.concat(chunks),
      }))
    })
    requestHandle.setTimeout(init.timeoutMs ?? 30000, () => {
      requestHandle.destroy(new Error(`request timeout: ${method} ${url}`))
    })
    requestHandle.on('error', reject)
    if (body !== undefined) requestHandle.write(body)
    requestHandle.end()
  })
  const durationMs = Date.now() - started
  const text = response.bytes.toString('utf8')
  let json = null
  try { json = JSON.parse(text) } catch { json = null }
  const raw = {
    status: response.status,
    headers: response.headers,
    bytes: response.bytes,
    text,
    json,
    contentType: String(response.headers['content-type'] ?? ''),
    url,
    method,
    durationMs,
  }
  if (observer) {
    observer({
      caseId: activeCase,
      target: target.label,
      method,
      path: `${parsed.pathname}${parsed.search}`,
      status: raw.status,
      contentType: raw.contentType,
      bytes: raw.bytes.length,
      durationMs,
    })
  }
  return raw
}

export function jsonHeaders(extra = {}) {
  return { 'Content-Type': 'application/json', ...extra }
}

export function multipartBody(fields, file) {
  const boundary = `----rainbowqa${randomUUID().replace(/-/g, '')}`
  const chunks = []
  for (const [key, value] of Object.entries(fields)) {
    chunks.push(Buffer.from(
      `--${boundary}\r\nContent-Disposition: form-data; name="${key}"\r\n\r\n${value}\r\n`, 'utf8'))
  }
  if (file) {
    chunks.push(Buffer.from(
      `--${boundary}\r\nContent-Disposition: form-data; name="${file.field}"; filename="${file.fileName}"\r\n` +
      `Content-Type: ${file.contentType}\r\n\r\n`, 'utf8'))
    chunks.push(file.bytes)
    chunks.push(Buffer.from('\r\n', 'utf8'))
  }
  chunks.push(Buffer.from(`--${boundary}--\r\n`, 'utf8'))
  return { body: Buffer.concat(chunks), contentType: `multipart/form-data; boundary=${boundary}` }
}

export class AdminClient {
  constructor(target, username, password) {
    this.target = target
    this.username = username
    this.password = password
    this.cookie = ''
    this.csrf = ''
  }

  async login() {
    const response = await request(this.target, '/api/v1/auth/login', {
      method: 'POST',
      headers: jsonHeaders({ Origin: this.target.origin }),
      body: { username: this.username, password: this.password },
    })
    if (response.status !== 200 || response.json?.code !== 0) {
      throw new Error(`admin login failed: HTTP=${response.status} ${response.json?.error_type ?? response.text.slice(0, 160)}`)
    }
    const cookies = response.headers['set-cookie']
    const first = Array.isArray(cookies) ? cookies[0] : cookies
    const cookie = String(first ?? '').split(';')[0]
    const csrf = response.json?.data?.csrf_token ?? ''
    if (!cookie || !csrf) throw new Error('admin login did not return a session cookie and csrf token')
    this.cookie = cookie
    this.csrf = csrf
    return response
  }

  async request(logicalPath, init = {}) {
    if (!this.cookie) await this.login()
    const method = (init.method ?? 'GET').toUpperCase()
    const headers = { ...(init.headers ?? {}) }
    headers.Cookie = this.cookie
    if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) {
      headers['X-CSRF-Token'] = this.csrf
      headers.Origin = this.target.origin
    }
    return request(this.target, logicalPath, { ...init, method, headers })
  }

  get(path) { return this.request(path) }
  post(path, body, headers = {}) {
    return this.request(path, { method: 'POST', headers: jsonHeaders(headers), body })
  }
  upload(path, fields, file) {
    const multipart = multipartBody(fields, file)
    return this.request(path, {
      method: 'POST',
      headers: { 'Content-Type': multipart.contentType },
      body: multipart.body,
    })
  }
}

export function assertOk(response, context, expectedStatus = 200) {
  if (response.status !== expectedStatus) {
    throw new Error(`${context}: expected HTTP ${expectedStatus}, got ${response.status}: ${response.text.slice(0, 500)}`)
  }
  return response
}

export function businessOk(response, context) {
  assertOk(response, context)
  if (response.json?.code !== 200) {
    throw new Error(`${context}: expected code 200, got ${response.json?.code}: ${response.text.slice(0, 500)}`)
  }
  return response.json.data
}

export function errorType(response) {
  return response.json?.error_type ?? ''
}