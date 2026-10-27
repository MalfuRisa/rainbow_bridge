import { getBasePath } from '@/lib/utils'
import type { BackupIntent, BackupKind } from '@/lib/api/backup'

export interface SavedBackup { jobs: Array<{ job_id: string; kind: BackupKind }>; pending: BackupIntent | null }
const key = () => `rainbow-bridge:${getBasePath()}:backup-tasks`
export function readBackupState(): SavedBackup {
  try {
    const saved = JSON.parse(sessionStorage.getItem(key()) || '{}')
    const jobs = Array.isArray(saved.jobs) ? saved.jobs.filter((job: { job_id?: string; kind?: string }) =>
      typeof job.job_id === 'string' && /^[\w-]{1,128}$/.test(job.job_id) && ['create', 'validate', 'restore'].includes(job.kind || '')).slice(-6) : []
    const pending = saved.pending && ['create', 'restore/preview', 'restore/execute', 'restore/cancel', 'delete'].includes(saved.pending.action) && typeof saved.pending.payload?.request_id === 'string' ? saved.pending : null
    return { jobs, pending }
  } catch { return { jobs: [], pending: null } }
}
export function saveBackupState(state: SavedBackup) {
  // Only task IDs and the original mutation intent. Never persist sessions or file bytes.
  sessionStorage.setItem(key(), JSON.stringify(state))
}
