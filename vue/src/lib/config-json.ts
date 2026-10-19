// Preserve original value tokens while editing key/value rows.
export function objectValueTokens(content: string): Map<string, string> {
  const parsed = JSON.parse(content)
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('JSON object required')
  const result = new Map<string, string>()
  let start = content.indexOf('{') + 1
  let depth = 0, quoted = false, escaped = false
  const consume = (end: number) => {
    const member = content.slice(start, end).trim()
    if (!member) return
    const keyMatch = member.match(/^"(?:\\.|[^"\\])*"\s*:/)
    if (!keyMatch) throw new Error('Invalid object member')
    const colon = keyMatch[0].lastIndexOf(':')
    const key = JSON.parse(member.slice(0, colon).trim()) as string
    result.set(key, member.slice(colon + 1).trim())
  }
  for (let i = start; i < content.length; i++) {
    const c = content[i]
    if (quoted) {
      if (escaped) escaped = false
      else if (c === '\\') escaped = true
      else if (c === '"') quoted = false
      continue
    }
    if (c === '"') quoted = true
    else if (c === '[' || c === '{') depth++
    else if (c === ']' || c === '}') {
      if (depth === 0) { consume(i); break }
      depth--
    } else if (c === ',' && depth === 0) { consume(i); start = i + 1 }
  }
  return result
}

export function tokenDisplay(token: string): string {
  return token.startsWith('"') ? JSON.parse(token) as string : token
}
