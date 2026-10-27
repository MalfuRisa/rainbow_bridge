import { ref, watch, onUnmounted, type WatchSource } from 'vue'
import { sessionIdentity, sessionGeneration } from '@/lib/auth/session'

interface HistoryPage<T> { items: T[]; has_more: boolean; next_before_event_id: string }
export function useDialogHistory<T>(
  source: WatchSource<readonly [boolean, string, string, number?]>,
  fetchPage: (environment: string, pipeline: string, before: string) => Promise<HistoryPage<T>>,
) {
  const items = ref<T[]>([])
  const hasMore = ref(false)
  const nextBefore = ref('')
  const loading = ref(false)
  const errorMessage = ref('')
  let epoch = 0
  let scope: readonly [boolean, string, string, number?] = [false, '', '']
  async function load(before = '') {
    if (!scope[0] || loading.value) return
    const started = epoch
    const identity = sessionGeneration()
    const [, env, pipeline] = scope
    loading.value = true; errorMessage.value = ''
    const current = () => started === epoch && identity === sessionGeneration()
    try {
      const page = await fetchPage(env, pipeline, before)
      if (!current()) return
      items.value = (before ? [...items.value, ...page.items] : page.items) as typeof items.value
      hasMore.value = page.has_more; nextBefore.value = page.next_before_event_id || ''
    } catch (cause) {
      if (current()) errorMessage.value = cause instanceof Error ? cause.message : '历史加载失败'
    } finally { if (current()) loading.value = false }
  }
  watch([source, sessionIdentity], ([value]) => {
    epoch++; scope = value; items.value = []; hasMore.value = false
    nextBefore.value = ''; errorMessage.value = ''; loading.value = false
    if (scope[0]) void load()
  }, { immediate: true })
  onUnmounted(() => { epoch++ })
  return { items, hasMore, nextBefore, loading, errorMessage, load }
}
