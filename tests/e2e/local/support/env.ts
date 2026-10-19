import * as fs from 'fs'
import * as path from 'path'

/**
 * RB_QA_* 环境契约：由 script/test-local.ps1 在每轮隔离运行中注入。
 * 缺失任何必需变量时立即抛错，禁止无声回退到默认地址。
 */
export interface Target {
  label: string
  origin: string
  prefix: string
  instanceDir: string
  pid: string
}

function required(name: string): string {
  const value = process.env[name]
  if (!value) {
    throw new Error(`缺少环境变量 ${name}：本套用例只能由 script/test-local.ps1 驱动运行`)
  }
  return value
}

export function artifactsDir(): string {
  return required('RB_QA_ARTIFACTS_DIR')
}

export function runDir(): string {
  return required('RB_QA_RUN_DIR')
}

export function runId(): string {
  return process.env.RB_QA_RUN_ID ?? 'unknown-run'
}

export function phase(): string {
  return process.env.RB_QA_PHASE ?? 'unknown-phase'
}

export function suiteName(): string {
  return process.env.RB_QA_SUITE ?? 'unknown'
}

export function dbtoolPath(): string {
  return required('RB_QA_DBTOOL')
}

/** 冻结测试管理令牌:只用于专项套件(脚本注入,绝不落盘)。 */

export function expectedStatePath(): string {
  return required('RB_QA_EXPECTED_STATE')
}

function targetFrom(
  originEnv: string,
  prefixEnv: string,
  pidEnv: string,
  instanceEnv: string,
  label: string,
): Target {
  return {
    label,
    origin: required(originEnv),
    prefix: process.env[prefixEnv] ?? '',
    pid: process.env[pidEnv] ?? '',
    instanceDir: required(instanceEnv),
  }
}

export const targetA = (): Target =>
  targetFrom('RB_QA_A_ORIGIN', 'RB_QA_A_PREFIX', 'RB_QA_A_PID', 'RB_QA_A_INSTANCE_DIR', 'a')

export const targetB = (): Target =>
  targetFrom('RB_QA_B_ORIGIN', 'RB_QA_B_PREFIX', 'RB_QA_B_PID', 'RB_QA_B_INSTANCE_DIR', 'b')

export const targetRootA = (): Target =>
  targetFrom('RB_QA_ROOT_A_ORIGIN', 'RB_QA_ROOT_A_PREFIX', '', 'RB_QA_ROOT_A_INSTANCE_DIR', 'root-a')

export const targetRootB = (): Target =>
  targetFrom('RB_QA_ROOT_B_ORIGIN', 'RB_QA_ROOT_B_PREFIX', '', 'RB_QA_ROOT_B_INSTANCE_DIR', 'root-b')

/** 拼接实例内绝对 URL：显式包含本部署前缀，绝不用裸 '/'。 */
export function url(target: Target, pathname: string): string {
  const prefix = target.prefix.endsWith('/') ? target.prefix.slice(0, -1) : target.prefix
  const suffix = pathname.startsWith('/') ? pathname : `/${pathname}`
  return `${target.origin}${prefix}${suffix}`
}

export function dbPath(target: Target): string {
  return path.join(target.instanceDir, 'data', 'resource.db')
}

export function uploadsDir(target: Target): string {
  return path.join(target.instanceDir, 'data', 'uploads')
}

export function dataDir(target: Target): string {
  return path.join(target.instanceDir, 'data')
}

/** 证据文件统一落到本轮 artifacts，文件名带阶段前缀。 */
export function writeEvidence(name: string, payload: unknown): string {
  const dir = artifactsDir()
  fs.mkdirSync(dir, { recursive: true })
  const file = path.join(dir, `${phase()}-${name}.json`)
  fs.writeFileSync(file, JSON.stringify(payload, null, 2))
  return file
}

export function readJsonFile<T>(file: string): T {
  return JSON.parse(fs.readFileSync(file, 'utf8')) as T
}