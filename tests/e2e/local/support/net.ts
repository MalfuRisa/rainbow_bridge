import { createHash } from 'crypto'
import { Target, url } from './env'

export interface RawResponse {
  status: number
  contentType: string
  bytes: Buffer
  text: string
  json: any
}

export interface MultipartFile {
  field: string
  filename: string
  contentType: string
  bytes: Buffer
}

export function sha256Hex(bytes: Buffer): string {
  return createHash('sha256').update(bytes).digest('hex')
}

/** 真实 HTTP 请求，直连本轮 127.0.0.1 实例；不使用框架内 PerformRequest。 */
export async function request(
  target: Target,
  pathname: string,
  init: RequestInit = {},
): Promise<RawResponse> {
  // resolveUrl 可能已返回绝对 URL；此时不得再拼接部署前缀(否则会构造出畸形 URL)
  const requestUrl = pathname.startsWith('http') ? pathname : url(target, pathname)
  const response = await fetch(requestUrl, init)
  const arrayBuffer = await response.arrayBuffer()
  const bytes = Buffer.from(arrayBuffer)
  const text = bytes.toString('utf8')
  let json: any = null
  try {
    json = JSON.parse(text)
  } catch {
    json = null
  }
  return {
    status: response.status,
    contentType: response.headers.get('content-type') ?? '',
    bytes,
    text,
    json,
  }
}

export async function getJson(
  target: Target,
  pathname: string,
  headers: Record<string, string> = {},
): Promise<RawResponse> {
  return request(target, pathname, { method: 'GET', headers })
}

export async function postJson(
  target: Target,
  pathname: string,
  body: unknown,
  headers: Record<string, string> = {},
): Promise<RawResponse> {
  return request(target, pathname, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...headers },
    body: JSON.stringify(body),
  })
}

/** 以原始字符串发送 JSON，避免测试端先做数字/转义加工(T06 精度样本需要)。 */
export async function postRawJson(
  target: Target,
  pathname: string,
  rawBody: string,
  headers: Record<string, string> = {},
): Promise<RawResponse> {
  return request(target, pathname, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...headers },
    body: rawBody,
  })
}

export async function postMultipart(
  target: Target,
  pathname: string,
  fields: Record<string, string>,
  files: MultipartFile[],
  headers: Record<string, string> = {},
): Promise<RawResponse> {
  const form = new FormData()
  for (const [key, value] of Object.entries(fields)) {
    form.append(key, value)
  }
  for (const file of files) {
    form.append(
      file.field,
      new Blob([new Uint8Array(file.bytes)], { type: file.contentType }),
      file.filename,
    )
  }
  return request(target, pathname, { method: 'POST', body: form as any, headers })
}

export function businessCode(response: RawResponse): number | undefined {
  const code = response.json?.code
  return typeof code === 'number' ? code : undefined
}

export function expectBusinessOk(response: RawResponse, context: string): void {
  if (businessCode(response) !== 200) {
    throw new Error(`${context}: 业务失败 HTTP=${response.status} body=${response.text.slice(0, 400)}`)
  }
}