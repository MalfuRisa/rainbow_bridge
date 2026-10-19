import { execFileSync } from 'child_process'
import { createHash } from 'crypto'
import * as fs from 'fs'
import { Target, dbPath, dbtoolPath } from './env'

export interface TableDump {
  table: string
  count: number
  rows: Record<string, unknown>[]
}

/**
 * 只读取库用于取证：与接口返回对照，不能用客户端结果替代查库。
 */
export function dumpTable(target: Target, table: string, filters: string[] = []): TableDump {
  const output = execFileSync(dbtoolPath(), ['dump', dbPath(target), table, ...filters], {
    encoding: 'utf8',
  })
  return JSON.parse(output) as TableDump
}

export function configsByAlias(target: Target, environmentKey: string, pipelineKey: string, alias: string): Record<string, unknown>[] {
  return dumpTable(target, 'resource_config', [
    `environment_key=${environmentKey}`,
    `pipeline_key=${pipelineKey}`,
    `alias=${alias}`,
  ]).rows
}

export function fileSha256(filePath: string): { size: number; sha256: string } {
  const bytes = fs.readFileSync(filePath)
  return { size: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') }
}