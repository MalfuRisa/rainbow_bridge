import { computed, ref } from 'vue'
import { useEnvironmentStore } from '@/stores/environment'
import { captureScope, assertEnvironment, assertScope, isScopeError } from '@/lib/scope'
import { ApiError } from '@/lib/api/error'
import { isSessionError } from '@/lib/api/http'

interface DeleteTarget { kind: 'environment' | 'pipeline'; key: string; name: string; environmentKey: string; environmentId: string; scopeId: string }
export function useScopeDeletion() {
  const environments = useEnvironmentStore()
  const target = ref<DeleteTarget | null>(null)
  const error = ref('')
  const deleting = ref(false)
  const uncertain = ref(false)
  const invalid = computed(() => {
    if (!target.value) return false
    const env = environments.environments.find(item => item.key === target.value!.environmentKey)
    return env?.environmentId !== target.value.environmentId || (target.value.kind === 'pipeline' &&
      env.pipelines.find(item => item.key === target.value!.key)?.scopeId !== target.value.scopeId)
  })
  function request(kind: DeleteTarget['kind'], key: string, name: string, environmentKey = key) {
    const env = environments.environments.find(item => item.key === environmentKey)
    target.value = { kind, key, name, environmentKey, environmentId: env?.environmentId || '',
      scopeId: kind === 'pipeline' ? captureScope(environmentKey, key).scopeId : '' }
    error.value = ''; uncertain.value = false
  }
  async function verify() {
    await environments.fetchOverview()
    if (environments.error) { error.value = environments.error; return }
    if (invalid.value) { error.value = '原对象已删除或被重新创建，请关闭后重新选择。'; return }
    uncertain.value = false
    error.value = '已核对原对象仍存在，可重新确认删除。'
  }
  async function confirm() {
    const selected = target.value
    if (!selected || deleting.value || uncertain.value || invalid.value) return
    deleting.value = true; error.value = ''
    try {
      if (selected.kind === 'environment') {
        assertEnvironment(selected.environmentKey, selected.environmentId)
        await environments.deleteEnvironment(selected.environmentKey, selected.environmentId)
      } else {
        assertScope(selected.environmentKey, selected.key, selected.scopeId)
        await environments.deletePipeline(selected.environmentKey, selected.key, selected.scopeId)
      }
      if (target.value === selected) target.value = null
    } catch (cause) {
      if (isSessionError(cause)) return
      error.value = cause instanceof Error ? cause.message : '删除失败，请重试。'
      if (isScopeError(cause)) uncertain.value = true
      else if (!(cause instanceof ApiError) || cause.code < 0 || cause.code >= 500) {
        uncertain.value = true
        error.value = '删除结果尚未确认，请先刷新环境核对原对象。'
      }
    } finally { deleting.value = false }
  }
  return { target, error, deleting, uncertain, invalid, request, confirm, verify }
}
