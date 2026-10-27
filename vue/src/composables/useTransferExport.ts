import { computed, onUnmounted, ref, watch, type Ref } from 'vue'
import { exportTransfer, previewTransfer, type ExportPreview, type ExportRequest } from '@/lib/api/transfer-export'

export function useTransferExport(request: Ref<ExportRequest | null>) {
  const preview = ref<ExportPreview | null>(null)
  const previewing = ref(false)
  const exporting = ref(false)
  const error = ref('')
  const success = ref(false)
  let batch = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false
  let shownRequest = ''
  function invalidate() {
    batch++; clearTimeout(timer); preview.value = null; shownRequest = ''; error.value = ''; success.value = false; previewing.value = false
  }
  async function loadPreview() {
    const selected = request.value
    if (!selected || stopped) return
    const current = ++batch
    const frozen = JSON.parse(JSON.stringify(selected)) as ExportRequest
    const signature = JSON.stringify(frozen)
    previewing.value = true; preview.value = null; error.value = ''; success.value = false
    try {
      const result = await previewTransfer(frozen)
      if (current !== batch || stopped) return
      preview.value = result; shownRequest = signature
    } catch (cause) { if (current === batch && !stopped) error.value = cause instanceof Error ? cause.message : '预览失败，请重试' }
    finally { if (current === batch && !stopped) previewing.value = false }
  }
  const canExport = computed(() => Boolean(preview.value && request.value && shownRequest === JSON.stringify(request.value) && !previewing.value && !exporting.value && !error.value))
  async function download() {
    if (!canExport.value || !request.value) return
    const selected = JSON.parse(shownRequest) as ExportRequest
    const current = batch
    exporting.value = true; error.value = ''; success.value = false
    try {
      const archive = await exportTransfer(selected)
      if (current !== batch || stopped) return
      const url = URL.createObjectURL(archive.blob)
      const anchor = document.createElement('a')
      anchor.href = url; anchor.download = archive.filename || `rainbow-transfer-${selected.source}.${selected.format}`
      document.body.append(anchor); anchor.click(); anchor.remove()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
      success.value = true
    } catch (cause) {
      if (current !== batch || stopped) return
      error.value = cause instanceof Error ? cause.message : '导出失败，请重试'
      preview.value = null; shownRequest = ''
    } finally { exporting.value = false }
  }
  watch(() => JSON.stringify(request.value), () => {
    invalidate()
    if (request.value) { previewing.value = true; timer = setTimeout(loadPreview, 250) }
  }, { immediate: true })
  onUnmounted(() => { stopped = true; batch++; clearTimeout(timer) })
  return { preview, previewing, exporting, error, success, canExport, loadPreview, download }
}
