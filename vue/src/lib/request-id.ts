/** Cryptographic UUID v4 for both secure origins and ordinary HTTP intranets. */
export function createRequestId(): string {
  const source = globalThis.crypto
  if (typeof source?.randomUUID === 'function') {
    try { return source.randomUUID() }
    catch { /* A browser can expose the method but deny its use; try secure bytes. */ }
  }
  if (typeof source?.getRandomValues === 'function') {
    try {
      const bytes = source.getRandomValues(new Uint8Array(16))
      bytes[6] = (bytes[6]! & 0x0f) | 0x40
      bytes[8] = (bytes[8]! & 0x3f) | 0x80
      const hex = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
      return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
    } catch { /* Never substitute a weak random source or partially generated ID. */ }
  }
  throw new Error('当前浏览器无法生成操作编号，请更换浏览器后重试。')
}
