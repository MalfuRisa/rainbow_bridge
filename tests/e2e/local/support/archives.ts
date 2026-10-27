import JSZip from 'jszip'
import * as tarStream from 'tar-stream'
import { createGzip, gunzipSync } from 'zlib'
import { Buffer } from 'buffer'

/**
 * 与后端 biz/service/transfer_service.go 一致的归档格式：
 *   configs.json -> {environments, pipelines, business_configs, assets}
 *   files/<file_id>/<file_name> -> 资源字节
 */

export interface ArchiveConfigRow {
  resource_key?: string
  alias?: string
  name?: string
  environment_key?: string
  pipeline_key?: string
  type: string
  content: unknown
  remark?: string
  is_perm?: boolean
  description?: string
}

export interface ArchiveFileEntry {
  fileId: string
  fileName: string
  bytes: Buffer
}

export interface ArchiveMeta {
  environments?: unknown[]
  pipelines?: unknown[]
  assets?: Record<string, unknown>[]
  /** 生成 configs.json 时整体剔除的顶层键(损坏包派生用) */
  omitTopKeys?: string[]
}

export function buildConfigsJson(
  configs: ArchiveConfigRow[],
  meta: ArchiveMeta = {},
): { json: Buffer; data: Record<string, unknown> } {
  const data: Record<string, unknown> = {
    environments: meta.environments ?? [],
    pipelines: meta.pipelines ?? [],
    business_configs: configs,
    assets: meta.assets ?? [],
  }
  for (const key of meta.omitTopKeys ?? []) {
    delete data[key]
  }
  return { json: Buffer.from(JSON.stringify(data, null, 2)), data }
}

export interface TransferZipOptions {
  configs: ArchiveConfigRow[]
  files?: ArchiveFileEntry[]
  meta?: ArchiveMeta
  /** 覆盖或追加任意条目(路径 -> 字节)，用于受损包与路径边界样本 */
  extraEntries?: Record<string, Buffer | string>
  /** 覆盖 configs.json 原始字节(坏 JSON 样本) */
  rawConfigsJson?: Buffer
  /** 完全替换 configs.json 条目(缺失元数据样本) */
  omitConfigsJson?: boolean
  /** 同名条目重复写入(zip 重复条目样本) */
  duplicateEntries?: boolean
}

export async function buildTransferZip(options: TransferZipOptions): Promise<Buffer> {
  const zip = new JSZip()
  const { json } = buildConfigsJson(options.configs, options.meta ?? {})
  if (!options.omitConfigsJson) {
    zip.file('configs.json', options.rawConfigsJson ?? json)
    if (options.duplicateEntries) {
      zip.file('configs.json', options.rawConfigsJson ?? json)
    }
  }
  for (const file of options.files ?? []) {
    zip.file(`files/${file.fileId}/${file.fileName}`, file.bytes)
    if (options.duplicateEntries) {
      zip.file(`files/${file.fileId}/${file.fileName}`, file.bytes)
    }
  }
  for (const [name, payload] of Object.entries(options.extraEntries ?? {})) {
    zip.file(name, payload)
  }
  return zip.generateAsync({ type: 'nodebuffer', compression: 'DEFLATE' })
}

export interface TransferTarOptions extends TransferZipOptions {}

export async function buildTransferTarGz(options: TransferTarOptions): Promise<Buffer> {
  const pack = tarStream.pack()
  const chunks: Buffer[] = []
  const finished = new Promise<void>((resolve, reject) => {
    pack.on('data', (chunk: Buffer) => chunks.push(chunk))
    pack.on('end', () => resolve())
    pack.on('error', reject)
  })
  const { json } = buildConfigsJson(options.configs, options.meta ?? {})
  if (!options.omitConfigsJson) {
    pack.entry({ name: 'configs.json' }, options.rawConfigsJson ?? json)
  }
  for (const file of options.files ?? []) {
    pack.entry({ name: `files/${file.fileId}/${file.fileName}` }, file.bytes)
  }
  for (const [name, payload] of Object.entries(options.extraEntries ?? {})) {
    pack.entry({ name }, typeof payload === 'string' ? Buffer.from(payload) : payload)
  }
  pack.finalize()
  await finished
  return createGzipBuffer(Buffer.concat(chunks))
}

function createGzipBuffer(input: Buffer): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const gzip = createGzip()
    const chunks: Buffer[] = []
    gzip.on('data', (chunk: Buffer) => chunks.push(chunk))
    gzip.on('end', () => resolve(Buffer.concat(chunks)))
    gzip.on('error', reject)
    gzip.end(input)
  })
}

export async function readZipEntries(data: Buffer): Promise<Map<string, Buffer>> {
  const zip = await JSZip.loadAsync(data)
  const entries = new Map<string, Buffer>()
  for (const [name, entry] of Object.entries(zip.files)) {
    if (entry.dir) continue
    entries.set(name, await entry.async('nodebuffer'))
  }
  return entries
}

export async function readTarGzEntries(data: Buffer): Promise<Map<string, Buffer>> {
  const raw = gunzipSync(data)
  const extract = tarStream.extract()
  const entries = new Map<string, Buffer>()
  const finished = new Promise<void>((resolve, reject) => {
    extract.on('entry', (header, stream, next) => {
      const chunks: Buffer[] = []
      stream.on('data', (chunk: Buffer) => chunks.push(chunk))
      stream.on('end', () => {
        entries.set(header.name, Buffer.concat(chunks))
        next()
      })
      stream.resume()
    })
    extract.on('finish', () => resolve())
    extract.on('error', reject)
  })
  extract.end(raw)
  await finished
  return entries
}

/** 截断 ZIP 中段：生成"截断压缩包"样本。 */
export function truncateMiddle(data: Buffer, keepRatio = 0.6): Buffer {
  const keep = Math.max(22, Math.floor(data.length * keepRatio))
  return data.subarray(0, keep)
}

/** 归档条目名归一化(Windows 版 Go 用 filepath.Join 会写成反斜杠)。 */
export function normalizeEntryName(name: string): string {
  return name.replace(/\\/g, '/')
}

export function findEntry(entries: Map<string, Buffer>, wanted: string): Buffer | undefined {
  const direct = entries.get(wanted)
  if (direct) return direct
  for (const [name, bytes] of entries) {
    if (normalizeEntryName(name) === wanted) return bytes
  }
  return undefined
}

export function usesPortableSeparators(entries: Map<string, Buffer>): boolean {
  return [...entries.keys()].every((name) => !name.includes('\\'))
}