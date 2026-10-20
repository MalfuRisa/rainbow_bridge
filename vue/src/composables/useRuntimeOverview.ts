import { reactive, ref } from 'vue'
import { runtimeApi } from '@/lib/api/runtime'
import { fromApiEnvironmentOverview } from '@/lib/api/transformers'
import type { Environment } from '@/lib/types'
import { toast } from 'vue-sonner'

/** Public home data must not be cleared by management-session invalidation. */
export function useRuntimeOverview() {
  const environments = ref<Environment[]>([])
  const loading = ref(false)
  async function fetchOverview() {
    loading.value = true
    try {
      const response = await runtimeApi.overview()
      environments.value = response.list.map(fromApiEnvironmentOverview)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '获取公开环境概览失败')
    } finally { loading.value = false }
  }
  return reactive({ environments, loading, fetchOverview })
}
