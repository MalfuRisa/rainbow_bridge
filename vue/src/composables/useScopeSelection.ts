import { computed, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import type { Environment } from '@/lib/types'

interface ScopeSelection { envKey: string; pipelineKey: string; environmentId?: string; scopeId?: string }
const storageKey = `rainbow-bridge:${import.meta.env.BASE_URL}:workspace-scope`
let remembered: ScopeSelection | null = null

function readSelection(): ScopeSelection | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(storageKey) || 'null')
    if (typeof value?.envKey === 'string' && typeof value?.pipelineKey === 'string') return value
  } catch { /* Selection still works when browser storage is unavailable. */ }
  return remembered
}

export function clearScopeSelection() {
  remembered = null
  try { sessionStorage.removeItem(storageKey) } catch { /* Memory is already cleared. */ }
}

/** One browsing session shares its last scope across the four workspaces.
 * Resolve only against loaded options; never request an unverified URL/storage scope.
 */
export function useScopeSelection(environments: () => Environment[]) {
  const route = useRoute()
  const pagePath = route.path
  const selection = ref<ScopeSelection>({ envKey: '', pipelineKey: '' })
  let initialized = false
  let stopped = false

  function resolve(preferred?: ScopeSelection | null): ScopeSelection {
    const options = environments()
    const env = options.find(item => item.key === preferred?.envKey) ||
      options.find(item => item.key === 'default') || options[0]
    const pipelines = env?.pipelines || []
    const pipeline = pipelines.find(item => env?.key === preferred?.envKey && item.key === preferred?.pipelineKey) ||
      pipelines.find(item => item.key === 'default') || pipelines[0]
    return { envKey: env?.key || '', pipelineKey: pipeline?.key || '', environmentId: env?.environmentId || '', scopeId: pipeline?.scopeId || '' }
  }

  function commit(value: ScopeSelection) {
    if (stopped) return
    if (JSON.stringify(value) !== JSON.stringify(selection.value)) selection.value = value
    if (!value.envKey) return
    remembered = { ...value }
    try { sessionStorage.setItem(storageKey, JSON.stringify(value)) } catch { /* Use in-memory fallback. */ }
  }

  function initialize() {
    if (stopped) return
    if (!environments().length) return
    initialized = true
    const env = environments().find(item => item.key === route.query.env)
    commit(resolve(env ? { envKey: env.key, pipelineKey: typeof route.query.pipeline === 'string' ? route.query.pipeline : '' } : readSelection()))
  }

  const envPipelineModel = computed({ get: () => selection.value, set: (value: ScopeSelection) => commit(resolve(value)) })
  const selectedEnvKey = computed({
    get: () => selection.value.envKey,
    set: (envKey: string) => commit(resolve({ envKey, pipelineKey: envKey === selection.value.envKey ? selection.value.pipelineKey : '' })),
  })
  const selectedPipelineKey = computed({
    get: () => selection.value.pipelineKey,
    set: (pipelineKey: string) => commit(resolve({ envKey: selection.value.envKey, pipelineKey })),
  })

  watch(environments, () => {
    if (initialized) commit(resolve(selection.value))
    else if (environments().length) initialize()
  }, { deep: true })
  watch([() => route.query.env, () => route.query.pipeline], () => {
    if (initialized && route.path === pagePath) initialize()
  })
  onUnmounted(() => { stopped = true })
  const selectedScopeId = computed(() => selection.value.scopeId || '')
  const selectedEnvironmentId = computed(() => selection.value.environmentId || '')
  return { selectedEnvKey, selectedPipelineKey, selectedScopeId, selectedEnvironmentId, envPipelineModel, initialize }
}
