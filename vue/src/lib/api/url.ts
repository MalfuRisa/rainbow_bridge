import { getApiBaseUrl, getBasePath } from '../utils'
export { getBasePath } from '../utils'

export function apiURL(path: string): URL {
  const base = getApiBaseUrl().replace(/\/$/, '')
  if (base) return new URL(base + path, window.location.origin)
  return new URL(getBasePath().replace(/\/$/, '') + path, window.location.origin)
}

export function isTrustedApiURL(url: URL): boolean {
  const root = apiURL('/api/v1/')
  return url.origin === root.origin && url.pathname.startsWith(root.pathname)
}
