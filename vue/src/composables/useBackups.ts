import { computed, onMounted, onUnmounted, ref } from 'vue'
import { backupApi, type BackupCapabilities, type BackupIntent, type BackupItem, type BackupJob, type RestorePreview } from '@/lib/api/backup'
import { ApiError } from '@/lib/api/error'
import { isSessionError } from '@/lib/api/http'
import { useAuthStore } from '@/stores/auth'
import { createRequestId } from '@/lib/request-id'
import { backupError, inProgress } from '@/components/backup/presentation'
import { readBackupState, saveBackupState } from './backupPersistence'

export function useBackups() {
  const saved = readBackupState()
  const auth = useAuthStore()
  const capabilities = ref<BackupCapabilities | null>(null)
  const items = ref<BackupItem[]>([])
  const jobs = ref<BackupJob[]>([])
  const preview = ref<RestorePreview | null>(null)
  const pending = ref<BackupIntent | null>(saved.pending)
  const file = ref<File | null>(null)
  const loading = ref(true)
  const listLoading = ref(false)
  const submitting = ref(false)
  const downloading = ref('')
  const error = ref('')
  const taskError = ref('')
  const storageError = ref('')
  const hasMore = ref(false)
  const cursor = ref('')
  const now = ref(Date.now())
  const unresolved = ref(saved.jobs.length > 0)
  let timer: ReturnType<typeof setTimeout> | undefined
  let expiryTimer: ReturnType<typeof setInterval> | undefined
  let stopped = false
  let failures = 0
  let polling = false
  let tracked = saved.jobs
  const completed = new Set<string>()
  const waitingRestart = computed(() => jobs.value.find(job => job.state === 'restart_required'))
  const busy = computed(() => submitting.value || Boolean(pending.value) || unresolved.value || jobs.value.some(job => inProgress(job) || job.state === 'restart_required'))
  const canOperate = computed(() => Boolean(capabilities.value?.supported) && !busy.value && !storageError.value)
  const canRestore = computed(() => canOperate.value && Boolean(preview.value?.valid && !preview.value.issues?.length && preview.value.backup &&
    preview.value.target_instance_id && preview.value.package_sha256 && Date.parse(preview.value.expires_at) > now.value))

  function persist() {
    try { saveBackupState({ jobs: tracked, pending: pending.value }); return true }
    catch { storageError.value = '浏览器无法保存任务进度，请启用本站存储后再操作。'; return false }
  }
  function acceptJob(job: BackupJob) {
    if (!job?.job_id || !['create', 'validate', 'restore'].includes(job.kind)) throw new Error('未取得有效任务，请确认原请求结果')
    jobs.value = [...jobs.value.filter(item => item.job_id !== job.job_id), job]
    tracked = [...tracked.filter(item => item.kind !== job.kind), { job_id: job.job_id, kind: job.kind }]
    jobs.value = jobs.value.filter(item => tracked.some(savedJob => savedJob.job_id === item.job_id))
    persist()
  }
  async function loadList(more = false) {
    if (stopped || !capabilities.value?.supported || listLoading.value) return
    listLoading.value = true
    try {
      const result = await backupApi.list(more ? cursor.value : '')
      if (stopped) return
      const previous = more ? items.value : []
      items.value = [...previous, ...(result.items || []).filter(item => !previous.some(existing => existing.backup_id === item.backup_id))]
      hasMore.value = result.has_more
      cursor.value = result.next_before_id || ''
    } catch (cause) { if (!stopped) error.value = backupError(cause) }
    finally { listLoading.value = false }
  }
  async function readPreview(id: string) {
    const result = await backupApi.preview(id)
    if (!stopped) preview.value = result
  }
  async function followCompletion(job: BackupJob) {
    if (job.state !== 'succeeded' || completed.has(job.job_id)) return
    if (job.kind === 'validate') {
      const id = job.preview_id || job.result?.preview_id
      if (id) {
        try { await readPreview(id) }
        catch (cause) {
          if (!(cause instanceof ApiError) || ![404, 410].includes(cause.code)) throw cause
          preview.value = null
          error.value = backupError(cause)
        }
      }
    }
    if (job.kind === 'create') await loadList()
    if (job.kind === 'restore') { preview.value = null; file.value = null }
    if (!stopped) completed.add(job.job_id)
  }
  function schedule() {
    clearTimeout(timer)
    if (stopped || !auth.authenticated || !capabilities.value?.supported) return
    const needsPoll = unresolved.value || jobs.value.some(job => inProgress(job) || job.state === 'restart_required') || Boolean(taskError.value)
    if (needsPoll) timer = setTimeout(poll, Math.min(30000, failures ? 2000 * 2 ** failures : waitingRestart.value ? 8000 : 1500))
  }
  async function poll() {
    if (polling || stopped || !auth.authenticated || !capabilities.value?.supported) return
    clearTimeout(timer)
    polling = true
    let failed = false
    try {
      for (const remembered of [...tracked]) {
        try {
          const job = await backupApi.status(remembered.job_id)
          if (stopped) return
          acceptJob(job)
          await followCompletion(job)
        } catch (cause) {
          failed = true
          if (stopped) return
          taskError.value = backupError(cause)
          if (isSessionError(cause)) break
        }
      }
      unresolved.value = failed
      if (!failed) { taskError.value = ''; failures = 0 }
      else failures++
    } finally { polling = false; schedule() }
  }
  async function refresh() {
    if (loading.value && capabilities.value) return
    loading.value = true
    error.value = ''
    try {
      const result = await backupApi.capabilities()
      if (stopped) return
      capabilities.value = result
      if (result.supported) { await loadList(); await poll() }
    } catch (cause) { if (!stopped) error.value = backupError(cause) }
    finally { loading.value = false }
  }
  function selectFile(selected?: File) {
    if (!selected) return
    const intent = pending.value
    if (intent) {
      if (intent.action !== 'restore/preview' || selected.name !== intent.file?.name || selected.size !== intent.file.size || selected.lastModified !== intent.file.lastModified) {
        error.value = '请保留原请求，重新选择上次上传的同一个备份文件。'
        return
      }
    } else if (!canOperate.value) return
    if (!selected.name.toLowerCase().endsWith('.zip')) { error.value = '请选择业务备份 ZIP 文件。'; return }
    if (capabilities.value && selected.size > capabilities.value.limits.package_bytes) { error.value = '文件超过当前部署允许的备份包大小。'; return }
    file.value = selected
    if (!intent) preview.value = null
    error.value = ''
  }
  async function sendPending() {
    if (submitting.value || !pending.value || stopped || !auth.authenticated || !capabilities.value?.supported || storageError.value) return false
    const intent = pending.value
    if (intent.action === 'restore/preview' && !file.value) { error.value = '请重新选择上次上传的原备份包，再确认请求结果。'; return false }
    submitting.value = true
    error.value = ''
    let result: Awaited<ReturnType<typeof backupApi.mutate>>
    try {
      result = await backupApi.mutate(intent, file.value || undefined)
      if (stopped) return false
      if (result.job) acceptJob(result.job)
      else if (!(intent.action === 'delete' && result.deleted) && !(intent.action === 'restore/cancel' && result.cancelled)) throw new Error('未取得有效结果，请确认原请求')
      pending.value = null
      persist()
    } catch (cause) {
      if (stopped) return false
      error.value = backupError(cause)
      // A transport/server failure may have happened AFTER acceptance. Keep its exact intent.
      // BACKUP_MAINTENANCE/UNSUPPORTED/SPACE are explicit precondition rejections.
      if (cause instanceof ApiError && ((cause.code >= 400 && cause.code < 500 && !isSessionError(cause)) ||
          ['BACKUP_MAINTENANCE', 'BACKUP_UNSUPPORTED', 'BACKUP_SPACE_INSUFFICIENT'].includes(cause.errorType))) {
        pending.value = null
        if (cause.errorType === 'BACKUP_PREVIEW_EXPIRED') preview.value = null
        persist()
      }
      return false
    } finally { submitting.value = false }
    // Follow-up failures never turn an already accepted mutation into an uncertain one.
    if (result.job) {
      try { await followCompletion(result.job) } catch (cause) { taskError.value = backupError(cause) }
      schedule()
    }
    if (intent.action === 'delete') await loadList()
    if (intent.action === 'restore/cancel') await poll()
    return true
  }
  function start(action: BackupIntent['action'], payload: Record<string, string>, selected?: File) {
    if (busy.value || !capabilities.value?.supported || storageError.value) return Promise.resolve(false)
    if (selected) file.value = selected
    try {
      pending.value = { action, payload: { ...payload, request_id: createRequestId() },
        ...(selected ? { file: { name: selected.name, size: selected.size, lastModified: selected.lastModified } } : {}) }
      if (!persist()) { pending.value = null; return Promise.resolve(false) }
      return sendPending()
    } catch (cause) { error.value = backupError(cause); return Promise.resolve(false) }
  }
  async function cancelRestore() {
    if (!waitingRestart.value || submitting.value || pending.value || unresolved.value) return false
    try {
      pending.value = { action: 'restore/cancel', payload: { job_id: waitingRestart.value.job_id, request_id: createRequestId() } }
      if (!persist()) { pending.value = null; return false }
      return sendPending()
    } catch (cause) { error.value = backupError(cause); return false }
  }
  async function download(item: BackupItem) {
    if (downloading.value || !capabilities.value?.supported) return
    downloading.value = item.backup_id
    error.value = ''
    try {
      const result = await backupApi.download(item.backup_id)
      if (stopped) return
      const url = URL.createObjectURL(result.blob)
      const anchor = document.createElement('a')
      anchor.href = url; anchor.download = result.filename
      document.body.append(anchor); anchor.click(); anchor.remove()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (cause) { if (!stopped) error.value = backupError(cause) }
    finally { downloading.value = '' }
  }
  onMounted(() => { void refresh(); expiryTimer = setInterval(() => { now.value = Date.now() }, 1000) })
  onUnmounted(() => { stopped = true; clearTimeout(timer); clearInterval(expiryTimer) })
  return { capabilities, items, jobs, preview, pending, file, loading, listLoading, submitting, downloading, error, taskError, storageError,
    hasMore, now, unresolved, busy, canOperate, canRestore, waitingRestart, refresh, poll, loadList, selectFile, sendPending, cancelRestore, download,
    create: (reason: string) => start('create', { reason }),
    upload: () => file.value ? start('restore/preview', {}, file.value) : Promise.resolve(false),
    remove: (item: BackupItem) => start('delete', { backup_id: item.backup_id, expected_sha256: item.sha256 }),
    execute: (reason: string) => canRestore.value && preview.value ? start('restore/execute', {
      preview_id: preview.value.preview_id, package_sha256: preview.value.package_sha256,
      target_instance_id: preview.value.target_instance_id, confirmation: 'RESTORE_ALL_BUSINESS_DATA', reason,
    }) : Promise.resolve(false),
  }
}
