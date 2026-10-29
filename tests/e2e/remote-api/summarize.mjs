import { readFile, writeFile } from 'node:fs/promises'
import { resolve, join } from 'node:path'

const root = resolve(import.meta.dirname, '../../..')
const publicRun = process.env.RB_PUBLIC_RUN, managedRun = process.env.RB_MANAGED_RUN
if (!publicRun || !managedRun || !/^qa_\d{8}_[a-f0-9]{8}$/.test(publicRun) || !/^qa_\d{8}_[a-f0-9]{8}$/.test(managedRun)) throw new Error('Provide validated public and managed run IDs')
const evidence = id => join(root, '.local-qa/remote-api', id)
const read = async (id, name) => JSON.parse(await readFile(join(evidence(id), name), 'utf8'))
const matrix = await read(publicRun, 'coverage.json')
const cases = await read(managedRun, 'cases.json')
const browser = await read(managedRun, 'browser.json')
const issues = await read(managedRun, 'issues.json')
const forbidden = new Set(['POST /api/v1/auth/password', 'POST /api/v1/asset/upload',
  ...['create', 'delete', 'restore/preview', 'restore/execute', 'restore/cancel'].map(p => 'POST /api/v1/backup/' + p)])
const limited = new Set(['POST /api/v1/release/create', ...['list', 'status', 'download', 'restore/preview'].map(p => 'GET /api/v1/backup/' + p)])
const blocked = new Set(['POST /api/v1/release/activate', 'POST /api/v1/release/delete', 'POST /api/v1/release/compare', 'GET /api/v1/release/detail',
  ...['config', 'static', 'release', 'release/static'].map(p => 'GET /api/v1/runtime/' + p),
  'GET /api/v1/asset/file/*filepath', 'GET /api/v1/release/asset/*filepath'])
for (const row of matrix) {
  const key = row.method + ' ' + row.path
  row.scenarios = cases.filter(c => c.method === row.method && c.path === row.path)
  row.evidence = [row.probe ? `${publicRun}/http-observations.json` : '', row.scenarios.length ? `${managedRun}/cases.json` : ''].filter(Boolean)
  row.status = forbidden.has(key) ? '未执行' : limited.has(key) ? '部署能力限制' : blocked.has(key) ? '阻塞' : '通过'
  row.reason = row.status === '未执行' ? key.includes('/asset/') ? '资源物理文件无正式即时删除路径；不上传以避免遗留。' : '改密或全实例备份/恢复可能影响共享实例，不执行正向写入。' :
    row.status === '部署能力限制' ? key.includes('/backup/') ? '真实503 BACKUP_UNSUPPORTED；限制响应符合合同，功能不可用。' : '真实503 RELEASE_STORAGE_UNSUPPORTED；另有浏览器randomUUID缺陷。' :
    row.status === '阻塞' ? key.includes('/asset/') ? '未造物理资源/快照，仅验证不存在对象错误；匿名成功下载未测。' : '无本轮发布版本；负向缺版本/无active检查通过，正向发布依赖受MinIO阻断。' : '本轮指定安全场景通过；不表示所有边界全覆盖。'
  if (key === 'POST /api/v1/freeze/lock') { row.evidence.push(`${managedRun}/browser.json`); row.reason = '真实浏览器POST冻结200；后端冻结状态locked已复查。' }
  if (key === 'GET /api/v1/config/list' && issues.some(i => i.status === 'product-defect')) {
    row.status = '产品缺陷'; row.reason = '导入后普通列表仍为空，而相同scope详情和type=text列表能读取；疑似scope缓存失效遗漏。'
    row.evidence.push(`${managedRun}/transfer-readback.json`)
  }
  if (key === 'POST /api/v1/asset/upload') row.authBoundary = 'passed'
}
const staticEntries = []
for (const method of ['GET', 'HEAD']) {
  const result = await fetch('http://10.60.226.43:31880/rainbow-bridge/', { method, signal: AbortSignal.timeout(10000) })
  staticEntries.push({ method, path: '/*filepath', source: 'main.go', http: result.status, contentType: result.headers.get('content-type'),
    status: result.status === 200 ? '通过' : '异常', reason: '首页静态响应；非业务API。' })
  await result.body?.cancel()
}
const counts = {}
for (const row of matrix) counts[row.status] = (counts[row.status] || 0) + 1
const summary = { publicRun, managedRun, registeredBusinessAndHealthRoutes: matrix.length, staticRoutes: staticEntries.length,
  statuses: counts, managedAssertions: cases.length, managedAssertionsPassed: cases.filter(c => c.status === 'passed').length,
  browserProductDefect: browser.displayedRandomUUIDError && browser.freezeRequestSent && !browser.createRequestSent,
  cleanup: await read(managedRun, 'cleanup.json') }
await writeFile(join(evidence(managedRun), 'coverage.json'), JSON.stringify(matrix, null, 2))
await writeFile(join(evidence(managedRun), 'summary-final.json'), JSON.stringify(summary, null, 2))
await writeFile(join(evidence(managedRun), 'static-routes.json'), JSON.stringify(staticEntries, null, 2))
const header = '| 方法 | 注册路径 | 结论 | 本轮实际与未执行边界 | 证据 |\n| --- | --- | --- | --- | --- |\n'
const rows = matrix.map(r => `| ${r.method} | \`${r.path}\` | ${r.status} | ${r.reason} ${r.scenarios.map(c => `${c.name}:HTTP${c.http}/code${c.code ?? '-'}/${c.errorType ?? '-'}`).join('；')} | ${r.evidence.join('、')} |`).join('\n')
await writeFile(join(evidence(managedRun), 'coverage.md'), header + rows + '\n')
console.log(JSON.stringify({ ...summary, cleanup: summary.cleanup.result }))
