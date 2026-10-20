const paths = new Set(['/', '/config', '/environments', '/resources', '/migration', '/import-export', '/releases'])
export function safeReturnTo(value: unknown): string {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') ||
      /[\\\u0000-\u0020]/.test(value)) return '/config'
  try {
    const url = new URL(value, 'https://rainbow.invalid')
    if (url.origin !== 'https://rainbow.invalid' || !paths.has(url.pathname)) return '/config'
    const query = new URLSearchParams()
    for (const key of ['env', 'pipeline']) {
      const part = url.searchParams.get(key)
      if (part && /^[\p{L}\p{N}_.-]{1,128}$/u.test(part)) query.set(key, part)
    }
    return url.pathname + (query.size ? '?' + query.toString() : '')
  } catch { return '/config' }
}
