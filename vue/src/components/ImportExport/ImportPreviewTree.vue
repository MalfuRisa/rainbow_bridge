<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { ChevronRight, ChevronDown, Folder, FolderOpen, FileText, Layers } from 'lucide-vue-next'
import Checkbox from '@/components/ui/checkbox/Checkbox.vue'
import Badge from '@/components/ui/badge/Badge.vue'
import type {
  ImportPreviewEnvironment,
  ImportPreviewPipeline,
  ImportPreviewConfig,
  ExportSelection,
} from '@/lib/api/transfer'

interface CheckedState {
  environments: Set<string>
  pipelines: Set<string>
  configs: Set<string>
}

function createEmptyCheckedState(): CheckedState {
  return {
    environments: new Set(),
    pipelines: new Set(),
    configs: new Set(),
  }
}

function cloneCheckedState(state: CheckedState): CheckedState {
  return {
    environments: new Set(state.environments),
    pipelines: new Set(state.pipelines),
    configs: new Set(state.configs),
  }
}

function createFullCheckedState(data: ImportPreviewEnvironment[]): CheckedState {
  const state = createEmptyCheckedState()
  data.forEach((env) => {
    state.environments.add(env.environment_key)
    const pipelines = env.pipelines || []
    pipelines.forEach((pipe) => {
      const pipeKey = `${env.environment_key}:${pipe.pipeline_key}`
      state.pipelines.add(pipeKey)
      const configs = pipe.configs || []
      configs.forEach((cfg) => {
        state.configs.add(`${pipeKey}:${cfg.resource_key}`)
      })
    })
  })
  return state
}

function calculateSelectionsFromChecked(
  data: ImportPreviewEnvironment[],
  checked: CheckedState
): ExportSelection[] {
  const selections: ExportSelection[] = []

  data.forEach((env) => {
    const envKey = env.environment_key
    const envChecked = checked.environments.has(envKey)
    const pipelines = env.pipelines || []

    if (envChecked) {
      selections.push({
        environment_key: envKey,
        pipeline_key: '',
        resource_keys: [],
      })
    } else {
      pipelines.forEach((pipe) => {
        const pipeKey = `${envKey}:${pipe.pipeline_key}`
        const pipeChecked = checked.pipelines.has(pipeKey)
        const configs = pipe.configs || []

        if (pipeChecked) {
          selections.push({
            environment_key: envKey,
            pipeline_key: pipe.pipeline_key,
            resource_keys: [],
          })
        } else {
          const selectedConfigs = configs
            .filter((cfg) => checked.configs.has(`${pipeKey}:${cfg.resource_key}`))
            .map((cfg) => cfg.resource_key)

          if (selectedConfigs.length > 0) {
            selections.push({
              environment_key: envKey,
              pipeline_key: pipe.pipeline_key,
              resource_keys: selectedConfigs,
            })
          }
        }
      })
    }
  })

  return selections
}

function getEnvCheckState(env: ImportPreviewEnvironment, checked: CheckedState) {
  if (checked.environments.has(env.environment_key)) {
    return { checked: true, indeterminate: false }
  }
  let hasChecked = false
  let hasUnchecked = false
  const pipelines = env.pipelines || []
  pipelines.forEach((pipe) => {
    const pipeKey = `${env.environment_key}:${pipe.pipeline_key}`
    if (checked.pipelines.has(pipeKey)) {
      hasChecked = true
    } else {
      const configs = pipe.configs || []
      configs.forEach((cfg) => {
        if (checked.configs.has(`${pipeKey}:${cfg.resource_key}`)) {
          hasChecked = true
        } else {
          hasUnchecked = true
        }
      })
    }
  })
  if (hasChecked && hasUnchecked) {
    return { checked: false, indeterminate: true }
  }
  if (hasChecked) {
    return { checked: true, indeterminate: false }
  }
  return { checked: false, indeterminate: false }
}

function getPipeCheckState(
  env: ImportPreviewEnvironment,
  pipe: ImportPreviewPipeline,
  checked: CheckedState
) {
  const pipeKey = `${env.environment_key}:${pipe.pipeline_key}`
  if (checked.pipelines.has(pipeKey)) {
    return { checked: true, indeterminate: false }
  }
  let hasChecked = false
  let hasUnchecked = false
  const configs = pipe.configs || []
  configs.forEach((cfg) => {
    if (checked.configs.has(`${pipeKey}:${cfg.resource_key}`)) {
      hasChecked = true
    } else {
      hasUnchecked = true
    }
  })
  if (hasChecked && hasUnchecked) {
    return { checked: false, indeterminate: true }
  }
  if (hasChecked) {
    return { checked: true, indeterminate: false }
  }
  return { checked: false, indeterminate: false }
}

interface ImportPreviewTreeProps {
  data: ImportPreviewEnvironment[]
}

const props = defineProps<ImportPreviewTreeProps>()

const emit = defineEmits<{
  change: [selections: ExportSelection[]]
}>()

const expandedNodes = ref<Set<string>>(new Set())
const checked = ref<CheckedState>(createEmptyCheckedState())

onMounted(() => {
  if (props.data.length > 0) {
    checked.value = createFullCheckedState(props.data)
  }
})

watch(
  () => props.data,
  (newData) => {
    if (newData.length > 0) {
      checked.value = createFullCheckedState(newData)
    } else {
      checked.value = createEmptyCheckedState()
    }
    expandedNodes.value = new Set()
  },
  { immediate: true }
)

function toggleExpand(nodeId: string) {
  const next = new Set(expandedNodes.value)
  if (next.has(nodeId)) {
    next.delete(nodeId)
  } else {
    next.add(nodeId)
  }
  expandedNodes.value = next
}

function handleEnvChange(env: ImportPreviewEnvironment, isChecked: boolean) {
  const next = cloneCheckedState(checked.value)
  const pipelines = env.pipelines || []

  if (isChecked) {
    next.environments.add(env.environment_key)
    pipelines.forEach((pipe) => {
      const pipeKey = `${env.environment_key}:${pipe.pipeline_key}`
      next.pipelines.add(pipeKey)
      const configs = pipe.configs || []
      configs.forEach((cfg) => {
        next.configs.add(`${pipeKey}:${cfg.resource_key}`)
      })
    })
  } else {
    next.environments.delete(env.environment_key)
    pipelines.forEach((pipe) => {
      const pipeKey = `${env.environment_key}:${pipe.pipeline_key}`
      next.pipelines.delete(pipeKey)
      const configs = pipe.configs || []
      configs.forEach((cfg) => {
        next.configs.delete(`${pipeKey}:${cfg.resource_key}`)
      })
    })
  }

  checked.value = next
}

function handlePipeChange(env: ImportPreviewEnvironment, pipe: ImportPreviewPipeline, isChecked: boolean) {
  const next = cloneCheckedState(checked.value)
  const pipeKey = `${env.environment_key}:${pipe.pipeline_key}`
  const configs = pipe.configs || []
  const pipelines = env.pipelines || []

  if (isChecked) {
    next.pipelines.add(pipeKey)
    configs.forEach((cfg) => {
      next.configs.add(`${pipeKey}:${cfg.resource_key}`)
    })
    const allPipesSelected = pipelines.every((p) =>
      next.pipelines.has(`${env.environment_key}:${p.pipeline_key}`)
    )
    if (allPipesSelected) {
      next.environments.add(env.environment_key)
    }
  } else {
    next.pipelines.delete(pipeKey)
    next.environments.delete(env.environment_key)
    configs.forEach((cfg) => {
      next.configs.delete(`${pipeKey}:${cfg.resource_key}`)
    })
  }

  checked.value = next
}

function handleConfigChange(
  env: ImportPreviewEnvironment,
  pipe: ImportPreviewPipeline,
  cfg: ImportPreviewConfig,
  isChecked: boolean
) {
  const next = cloneCheckedState(checked.value)
  const pipeKey = `${env.environment_key}:${pipe.pipeline_key}`
  const cfgKey = `${pipeKey}:${cfg.resource_key}`
  const configs = pipe.configs || []
  const pipelines = env.pipelines || []

  if (isChecked) {
    next.configs.add(cfgKey)
    const allConfigsSelected = configs.every((c) =>
      next.configs.has(`${pipeKey}:${c.resource_key}`)
    )
    if (allConfigsSelected) {
      next.pipelines.add(pipeKey)
      const allPipesSelected = pipelines.every((p) =>
        next.pipelines.has(`${env.environment_key}:${p.pipeline_key}`)
      )
      if (allPipesSelected) {
        next.environments.add(env.environment_key)
      }
    }
  } else {
    next.configs.delete(cfgKey)
    next.pipelines.delete(pipeKey)
    next.environments.delete(env.environment_key)
  }

  checked.value = next
}

watch(
  () => checked.value,
  (newChecked) => {
    const selections = calculateSelectionsFromChecked(props.data, newChecked)
    emit('change', selections)
  },
  { deep: true }
)

function getEnvState(env: ImportPreviewEnvironment) {
  return getEnvCheckState(env, checked.value)
}

function getPipeState(env: ImportPreviewEnvironment, pipe: ImportPreviewPipeline) {
  return getPipeCheckState(env, pipe, checked.value)
}

function isConfigChecked(env: ImportPreviewEnvironment, pipe: ImportPreviewPipeline, cfg: ImportPreviewConfig) {
  const pipeKey = `${env.environment_key}:${pipe.pipeline_key}`
  return checked.value.configs.has(`${pipeKey}:${cfg.resource_key}`)
}

function isNodeExpanded(nodeId: string) {
  return expandedNodes.value.has(nodeId)
}

function getEnvConfigCount(env: ImportPreviewEnvironment) {
  return (env.pipelines || []).reduce((sum, p) => sum + (p.configs?.length || 0), 0)
}

function getStatusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  switch (status) {
    case 'new':
      return 'default'
    case 'exists':
      return 'secondary'
    case 'conflict':
      return 'destructive'
    default:
      return 'outline'
  }
}

function getStatusClass(status: string): string {
  switch (status) {
    case 'new':
      return 'spectrum-state-pill is-active'
    case 'conflict':
      return 'spectrum-state-pill is-frozen'
    case 'exists':
      return 'spectrum-state-pill is-idle'
    default:
      return ''
  }
}

function getStatusLabel(status: string): string {
  switch (status) {
    case 'new':
      return '新增'
    case 'exists':
      return '已存在'
    case 'conflict':
      return '冲突'
    default:
      return status
  }
}
</script>

<template>
  <div class="spectrum-transfer-tree space-y-1">
    <div v-if="data.length === 0" class="flex flex-col items-center justify-center py-8 text-muted-foreground">
      <Layers class="w-12 h-12 mb-4 opacity-50" />
      <p>暂无数据</p>
    </div>

    <template v-else>
      <div v-for="env in data" :key="env.environment_key" class="select-none">
        <div class="flex items-center gap-2 py-2 px-2 hover:bg-muted/50 rounded-md">
          <button
            @click="toggleExpand(env.environment_key)"
            :aria-expanded="isNodeExpanded(env.environment_key)"
            :aria-label="`展开或收起环境 ${env.environment_name}`"
            class="p-0.5 hover:bg-muted rounded"
          >
            <ChevronDown v-if="isNodeExpanded(env.environment_key)" class="w-4 h-4" />
            <ChevronRight v-else class="w-4 h-4" />
          </button>
          <Checkbox
            :model-value="getEnvState(env).checked"
            :aria-label="`选择环境 ${env.environment_name}`"
            @update:model-value="(v: boolean) => handleEnvChange(env, v)"
            :class="getEnvState(env).indeterminate ? 'data-[state=checked]:bg-primary/50' : ''"
          />
          <FolderOpen v-if="isNodeExpanded(env.environment_key)" class="w-4 h-4 text-muted-foreground" />
          <Folder v-else class="w-4 h-4 text-muted-foreground" />
          <span class="flex-1 text-sm font-medium">{{ env.environment_name }}</span>
          <Badge :variant="getStatusVariant(env.status)" :class="getStatusClass(env.status)" class="text-xs">
            {{ getStatusLabel(env.status) }}
          </Badge>
          <Badge variant="outline" class="text-xs ml-1">
            {{ (env.pipelines || []).length }} 渠道 / {{ getEnvConfigCount(env) }} 配置
          </Badge>
        </div>

        <div v-if="isNodeExpanded(env.environment_key)" class="ml-6">
          <div v-for="pipe in env.pipelines" :key="`${env.environment_key}:${pipe.pipeline_key}`">
            <div class="flex items-center gap-2 py-2 px-2 hover:bg-muted/50 rounded-md">
              <button
                @click="toggleExpand(`${env.environment_key}:${pipe.pipeline_key}`)"
                :aria-expanded="isNodeExpanded(`${env.environment_key}:${pipe.pipeline_key}`)"
                :aria-label="`展开或收起渠道 ${pipe.pipeline_name}`"
                class="p-0.5 hover:bg-muted rounded"
              >
                <ChevronDown v-if="isNodeExpanded(`${env.environment_key}:${pipe.pipeline_key}`)" class="w-4 h-4" />
                <ChevronRight v-else class="w-4 h-4" />
              </button>
              <Checkbox
                :model-value="getPipeState(env, pipe).checked"
                :aria-label="`选择渠道 ${pipe.pipeline_name}`"
                @update:model-value="(v: boolean) => handlePipeChange(env, pipe, v)"
                :class="getPipeState(env, pipe).indeterminate ? 'data-[state=checked]:bg-primary/50' : ''"
              />
              <Folder class="w-4 h-4 text-muted-foreground" />
              <span class="flex-1 text-sm">{{ pipe.pipeline_name }}</span>
              <Badge :variant="getStatusVariant(pipe.status)" :class="getStatusClass(pipe.status)" class="text-xs">
                {{ getStatusLabel(pipe.status) }}
              </Badge>
              <Badge variant="outline" class="text-xs ml-1">
                {{ (pipe.configs || []).length }} 配置
              </Badge>
            </div>

            <div v-if="isNodeExpanded(`${env.environment_key}:${pipe.pipeline_key}`) && pipe.configs?.length" class="ml-6">
              <div
                v-for="cfg in pipe.configs"
                :key="`${env.environment_key}:${pipe.pipeline_key}:${cfg.resource_key}`"
                class="flex items-center gap-2 py-1.5 px-2 hover:bg-muted/50 rounded-md"
              >
                <div class="w-5" />
                <Checkbox
                  :model-value="isConfigChecked(env, pipe, cfg)"
                  :aria-label="`选择配置 ${cfg.name || cfg.alias}`"
                  @update:model-value="(v: boolean) => handleConfigChange(env, pipe, cfg, v)"
                />
                <FileText class="w-4 h-4 text-muted-foreground" />
                <span class="flex-1 text-sm text-muted-foreground">
                  {{ cfg.name || cfg.alias }}
                </span>
                <Badge :variant="getStatusVariant(cfg.status)" :class="getStatusClass(cfg.status)" class="text-xs">
                  {{ getStatusLabel(cfg.status) }}
                </Badge>
                <Badge variant="outline" class="text-xs ml-1">
                  {{ cfg.type }}
                </Badge>
              </div>
            </div>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>
