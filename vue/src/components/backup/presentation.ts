import type { BackupJob, BackupCapabilities } from '@/lib/api/backup'
import { ApiError } from '@/lib/api/error'

export const backupTime = (value?: string) => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
export function backupSize(size: number) {
  if (size >= 1024 ** 3) return `${(size / 1024 ** 3).toFixed(1)} GiB`
  if (size >= 1024 ** 2) return `${(size / 1024 ** 2).toFixed(1)} MiB`
  return `${Math.max(1, Math.ceil(size / 1024))} KiB`
}
export const inProgress = (job: BackupJob) => ['queued', 'running'].includes(job.state)
export function jobTitle(job: BackupJob) {
  const name = { create: '备份', validate: '备份包校验', restore: '恢复' }[job.kind]
  const states = { queued: '等待处理', running: '处理中', succeeded: '完成', failed: '失败', interrupted: '已中断', restart_required: '等待服务重启', cancelled: '已取消' }
  return job.state === 'restart_required' ? '等待服务重启' : `${name}${states[job.state] || '状态待确认'}`
}
const phases: Record<string, string> = {
  waiting: '等待任务开始', draining_writes: '暂时暂停业务写入，等待已有操作完成', snapshotting_database: '保存一致的业务数据',
  copying_resources: '复制配套资源，业务写入暂时暂停', packaging: '正在打包，业务写入已恢复', validating_package: '校验文件与业务数据',
  preparing_candidate: '准备恢复数据', waiting_for_restart: '恢复计划已准备好，当前读取仍为原数据', completed: '备份包已生成',
  validated: '校验结束，请核对恢复预览', cancelled: '计划已取消，业务写入已恢复', committed: '数据已恢复，请重新核对业务内容',
}
export const jobPhase = (job: BackupJob) => phases[job.phase] || (inProgress(job) ? '任务处理中' : '以任务结果为准')
const errors: Record<string, string> = {
  BACKUP_BUSY: '已有备份或恢复任务，请稍后重试。', BACKUP_IN_USE: '该备份正在下载或被恢复计划引用，暂时不能删除。',
  BACKUP_MAINTENANCE: '备份或恢复正在暂停业务写入，请稍后重试；当前输入已保留。', BACKUP_SPACE_INSUFFICIENT: '服务器可用空间不足，请联系部署人员处理。',
  BACKUP_PREVIEW_EXPIRED: '预览已过期，请重新上传备份包。', BACKUP_PACKAGE_INVALID: '备份包校验未通过，请使用完整、可信的业务备份包。',
  BACKUP_LIMIT_EXCEEDED: '备份包或文件数量超出服务限额。', BACKUP_UNSUPPORTED: '当前部署暂不支持业务备份与恢复。',
  BACKUP_TARGET_MISMATCH: '备份或目标实例已变化，请重新校验。', BACKUP_NOT_FOUND: '备份不存在，请刷新列表。',
  BACKUP_PREVIEW_NOT_FOUND: '预览不存在，请重新上传。', BACKUP_STATE_CONFLICT: '当前恢复状态不允许此操作，请查询恢复结果后再处理。',
  BACKUP_RESTORE_PENDING: '已有恢复计划等待重启。请重启服务完成恢复，或取消待恢复计划后再操作。',
  BACKUP_ROLLBACK_REVIEW_REQUIRED: '恢复前的数据副本存在异常或安全状态无法确认，请联系部署人员核对处理后再恢复。刷新页面不能解除此限制。',
  BACKUP_REQUEST_CONFLICT: '该请求已使用过不同内容，请先核对上次任务。', BACKUP_STORAGE_ERROR: '备份存储不可用，请联系部署人员检查。',
}
export function backupError(cause: unknown) {
  if (!(cause instanceof ApiError)) return cause instanceof Error ? cause.message : '操作失败，请重试'
  // Keep the actual cause visible while an older backend is still running.
  if (cause.errorType === 'BACKUP_STATE_CONFLICT') {
    if (cause.message === 'an earlier rollback copy must be reviewed before another restore') return errors.BACKUP_ROLLBACK_REVIEW_REQUIRED!
    if (cause.message === 'another restore plan is already waiting for a restart') return errors.BACKUP_RESTORE_PENDING!
  }
  return errors[cause.errorType] || (cause.code < 0 ? '连接中断，尚不能确认操作结果。' : cause.message)
}
export function rollbackRetentionNotice(capabilities: BackupCapabilities | null) {
  const policy = capabilities?.rollback_retention
  return capabilities?.supported && policy?.managed === true && Number.isInteger(policy.keep_count) && policy.keep_count >= 1 && policy.keep_count <= 20
    ? `系统自动保留最近 ${policy.keep_count} 次恢复前的数据，成功留存的副本不影响再次恢复。`
    : ''
}
const reasons: Record<string, string> = {
  backup_disabled: '部署尚未启用业务备份。', database_driver_not_supported: '当前数据库不受支持，第一版仅支持 SQLite 文件库。',
  storage_not_local: '资源需使用本地存储。', redis_enabled: '需关闭 Redis。',
  database_outside_business_root: '数据库目录不符合备份要求。', release_snapshots_outside_business_root: '版本资源目录不符合备份要求。',
  backup_control_dir_different_volume: '备份控制目录需与业务数据位于同一磁盘卷。',
}
export const supportReason = (code: string) => reasons[code] || `部署条件未满足：${code}`
export function previewNotice(value: string) {
  if (value.startsWith('external_references_not_included:')) return `有 ${value.split(':')[1]} 处外部资源引用不包含在包内，恢复后仍需外部地址可用。`
  const descriptions: Record<string, string> = {
    unsupported_package_format: '包格式不受支持，请选择虹桥业务备份 ZIP。', unsupported_schema_version: '备份版本与当前服务不兼容。',
    database_integrity_check_failed: '业务数据完整性校验失败。', manifest_counts_mismatch: '备份清单与数据数量不一致。',
    unsafe_entry_path: '备份包包含不安全的文件路径。',
  }
  return descriptions[value] || value
}
