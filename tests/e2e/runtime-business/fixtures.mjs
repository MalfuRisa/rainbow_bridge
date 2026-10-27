import { createHash } from 'node:crypto'
import zlib from 'node:zlib'

const CRC_TABLE = (() => {
  const table = new Uint32Array(256)
  for (let n = 0; n < 256; n++) {
    let value = n
    for (let bit = 0; bit < 8; bit++) value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1
    table[n] = value >>> 0
  }
  return table
})()

function crc32(buffer) {
  let crc = 0xffffffff
  for (const byte of buffer) crc = CRC_TABLE[(crc ^ byte) & 0xff] ^ (crc >>> 8)
  return (crc ^ 0xffffffff) >>> 0
}

function chunk(type, data) {
  const typeBytes = Buffer.from(type, 'ascii')
  const length = Buffer.alloc(4)
  length.writeUInt32BE(data.length)
  const crc = Buffer.alloc(4)
  crc.writeUInt32BE(crc32(Buffer.concat([typeBytes, data])))
  return Buffer.concat([length, typeBytes, data, crc])
}

/** Build a real 8x8 RGB PNG so browser naturalWidth can prove decoding. */
export function makePng([red, green, blue]) {
  const width = 8
  const height = 8
  const header = Buffer.alloc(13)
  header.writeUInt32BE(width, 0)
  header.writeUInt32BE(height, 4)
  header[8] = 8
  header[9] = 2
  const rows = []
  for (let y = 0; y < height; y++) {
    const row = Buffer.alloc(1 + width * 3)
    for (let x = 0; x < width; x++) {
      row[1 + x * 3] = red
      row[2 + x * 3] = green
      row[3 + x * 3] = blue
    }
    rows.push(row)
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', header),
    chunk('IDAT', zlib.deflateSync(Buffer.concat(rows), { level: 9 })),
    chunk('IEND', Buffer.alloc(0)),
  ])
}

export function sha256(bytes) {
  return createHash('sha256').update(bytes).digest('hex')
}

export function mimeOf(bytes) {
  if (bytes.length > 8 && bytes.subarray(1, 4).toString() === 'PNG') return 'image/png'
  if (bytes.length > 4 && bytes.subarray(0, 4).toString() === '%PDF') return 'application/pdf'
  return 'text/plain'
}

export function buildAssets(runId) {
  const text = Buffer.from(`runtime-business-${runId} text asset\n`, 'utf8')
  const images = {
    red: makePng([220, 30, 40]),
    blue: makePng([30, 80, 220]),
    green: makePng([20, 180, 80]),
    yellow: makePng([230, 200, 20]),
    purple: makePng([130, 40, 180]),
  }
  const assets = {
    red: { bytes: images.red, fileName: 'red.png', contentType: 'image/png', sha256: sha256(images.red) },
    blue: { bytes: images.blue, fileName: 'blue.png', contentType: 'image/png', sha256: sha256(images.blue) },
    green: { bytes: images.green, fileName: 'green.png', contentType: 'image/png', sha256: sha256(images.green) },
    yellow: { bytes: images.yellow, fileName: 'yellow.png', contentType: 'image/png', sha256: sha256(images.yellow) },
    purple: { bytes: images.purple, fileName: 'purple.png', contentType: 'image/png', sha256: sha256(images.purple) },
    text: { bytes: text, fileName: 'notes.txt', contentType: 'text/plain', sha256: sha256(text) },
  }
  for (const asset of Object.values(assets)) asset.length = asset.bytes.length
  return assets
}

function objectContent(marker, imageRef, indent = false) {
  const separator = indent ? '\n  ' : ''
  return `{${separator}"label":"${marker}",${separator}"big":9007199254740993,${separator}` +
    `"small":9007199254740992,${separator}"image":"${imageRef}"${separator}}`
}

function richContent(marker, imageRef) {
  return `<p>${marker}</p><img src="${imageRef}" alt="${marker}">`
}

/**
 * Build the 11 required config types. stage selects the fixture revision:
 * a1 fixes the expected A1 values, a2/a3 only change the user-visible markers.
 */
export function buildConfigs(scope, assets, stage) {
  const marker = stage === 'a1' ? 'A_RELEASE_ONE'
    : stage === 'a2' ? 'A_RELEASE_TWO'
      : 'A_EDITOR_ONLY'
  const image = stage === 'a1' ? assets.red : stage === 'a2' ? assets.blue : assets.green
  const imageRef = `asset://${image.fileId}`
  const fileRef = `asset://${assets.text.fileId}`
  return [
    { alias: 'greeting', name: 'Greeting', type: 'text', content: marker },
    { alias: 'note', name: 'Note', type: 'textarea', content: `${marker} line one\n${marker} line two` },
    { alias: 'zero', name: 'Zero', type: 'number', content: '0' },
    { alias: 'decimal', name: 'Decimal', type: 'decimal', content: '0.00' },
    { alias: 'flag', name: 'Flag', type: 'boolean', content: 'false' },
    { alias: 'theme', name: 'Theme', type: 'color', content: '#112233' },
    { alias: 'obj', name: 'Object', type: 'object', content: objectContent(marker, imageRef, true) },
    { alias: 'kv', name: 'Key Value', type: 'keyvalue', content: objectContent(marker, imageRef) },
    { alias: 'banner', name: 'Banner', type: 'image', content: imageRef },
    { alias: 'attachment', name: 'Attachment', type: 'file', content: fileRef },
    { alias: 'rich', name: 'Rich Text', type: 'richtext', content: richContent(marker, imageRef) },
  ]
}

export function buildSimpleConfigs(scope, assets, marker, imageKey) {
  return [
    { alias: 'greeting', name: 'Greeting', type: 'text', content: marker },
    { alias: 'banner', name: 'Banner', type: 'image', content: `asset://${assets[imageKey].fileId}` },
    { alias: 'rich', name: 'Rich Text', type: 'richtext', content: richContent(marker, `asset://${assets[imageKey].fileId}`) },
  ]
}

export function configsByAlias(runtimeData) {
  const result = new Map()
  for (const config of runtimeData?.configs ?? []) result.set(config.alias, config)
  return result
}

export function bigIntegerLiterals(text) {
  return {
    large: text.includes('9007199254740993'),
    neighbor: text.includes('9007199254740992'),
  }
}

export function assetPartsFromUrl(value) {
  const parsed = new URL(value, 'http://invalid.local')
  const match = parsed.pathname.match(/\/api\/v1\/release\/asset\/([^/]+)\/([^/]+)$/)
  if (!match) return null
  return { snapshotId: match[1], assetId: decodeURIComponent(match[2]), pathname: parsed.pathname, search: parsed.search }
}

export function resolveReturnedUrl(origin, value) {
  if (/^https?:\/\//i.test(value)) return new URL(value)
  if (value.startsWith('/')) return new URL(origin + value)
  return new URL(value, origin + '/')
}