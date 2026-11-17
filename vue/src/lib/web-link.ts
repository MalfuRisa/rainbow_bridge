export interface WebLinkContent {
  url: string
  icon_url?: string
  sort_order?: number
}

export function parseWebLinkContent(content: string): WebLinkContent | null {
  if (typeof content !== 'string' || !content.trim()) return null
  try {
    const parsed: unknown = JSON.parse(content)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null
    return parsed as WebLinkContent
  } catch {
    return null
  }
}

/** 与服务端 web_link 校验保持一致：仅接受无用户信息的绝对 HTTP(S) 地址。 */
export function isValidWebLinkUrl(value: string): boolean {
  const raw = value.trim()
  if (!raw || raw.includes('\\') || /[\u0000-\u001f\u007f-\u009f]/.test(raw)) return false
  try {
    const parsed = new URL(raw)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return false
    if (!parsed.hostname || parsed.username || parsed.password) return false
    if (parsed.port) {
      const port = Number(parsed.port)
      if (!Number.isInteger(port) || port < 1 || port > 65535) return false
    }
    return true
  } catch {
    return false
  }
}