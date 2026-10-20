<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { ChevronRight, ChevronDown, Folder, FolderOpen, FileText, Layers } from 'lucide-vue-next'
import Checkbox from '@/components/ui/checkbox/Checkbox.vue'
import Badge from '@/components/ui/badge/Badge.vue'
import type {
  ExportTreeEnvironment,
  ExportTreePipeline,
  ExportTreeConfig,
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

function calculateSelectionsFromChecked(
  data: ExportTreeEnvironment[],
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

function getEnvCheckState(env: ExportTreeEnvironment, checked: CheckedState) {
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
  env: ExportTreeEnvironment,
  pipe: ExportTreePipeline,
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

interface ExportTreeSelectProps {
  data: ExportTreeEnvironment[]
  loading?: boolean
}

const props = defineProps<ExportTreeSelectProps>()

const emit = defineEmits<{
  change: [selections: ExportSelection[]]
}>()

const expandedNodes = ref<Set<string>>(new Set())
const checked = ref<CheckedState>(createEmptyCheckedState())

function toggleExpand(nodeId: string) {
  const next = new Set(expandedNodes.value)
  if (next.has(nodeId)) {
    next.delete(nodeId)
  } else {
    next.add(nodeId)
  }
  expandedNodes.value = next
}

function handleEnvChange(env: ExportTreeEnvironment, isChecked: boolean) {
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

function handlePipeChange(env: ExportTreeEnvironment, pipe: ExportTreePipeline, isChecked: boolean) {
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
  env: ExportTreeEnvironment,
  pipe: ExportTreePipeline,
  cfg: ExportTreeConfig,
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

const stats = computed(() => {
  let envs = 0
  let pipes = 0
  let configs = 0
  let selected = 0

  props.data.forEach((env) => {
    envs++
    const pipelines = env.pipelines || []
    pipelines.forEach((pipe) => {
      pipes++
      const pipeKey = `${env.environment_key}:${pipe.pipeline_key}`
      const pipeConfigs = pipe.configs || []
      pipeConfigs.forEach((cfg) => {
        configs++
        if (checked.value.configs.has(`${pipeKey}:${cfg.resource_key}`)) {
          selected++
        }
      })
    })
  })

  return { totalEnvs: envs, totalPipes: pipes, totalConfigs: configs, selectedConfigs: selected }
})

watch(
  () => checked.value,
  (newChecked) => {
    const selections = calculateSelectionsFromChecked(props.data, newChecked)
    emit('change', selections)
  },
  { deep: true }
)

watch(
  () => props.data,
  () => {
    checked.value = createEmptyCheckedState()
    expandedNodes.value = new Set()
  }
)

function getEnvState(env: ExportTreeEnvironment) {
  return getEnvCheckState(env, checked.value)
}

function getPipeState(env: ExportTreeEnvironment, pipe: ExportTreePipeline) {
  return getPipeCheckState(env, pipe, checked.value)
}

function isConfigChecked(env: ExportTreeEnvironment, pipe: ExportTreePipeline, cfg: ExportTreeConfig) {
  const pipeKey = `${env.environment_key}:${pipe.pipeline_key}`
  return checked.value.configs.has(`${pipeKey}:${cfg.resource_key}`)
}

function isNodeExpanded(nodeId: string) {
  return expandedNodes.value.has(nodeId)
}

function getEnvConfigCount(env: ExportTreeEnvironment) {
  return (env.pipelines || []).reduce((sum, p) => sum + (p.config_count || 0), 0)
}
</script>

<template>
  <div class="spectrum-transfer-tree space-y-1">
    <div v-if="loading" class="flex items-center justify-center py-8 text-muted-foreground">
      <div class="animate-spin rounded-full h-6 w-6 border-b-2 border-primary mr-2" />
      加载中...
    </div>

    <div v-else-if="data.length === 0" class="flex flex-col items-center justify-center py-8 text-muted-foreground">
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
          <Badge variant="secondary" class="text-xs">
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
              <Badge variant="outline" class="text-xs">
                {{ pipe.config_count || 0 }} 配置
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
                <Badge variant="outline" class="text-xs">
                  {{ cfg.type }}
                </Badge>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="pt-4 mt-4 border-t text-sm text-muted-foreground">
        共 {{ stats.totalEnvs }} 个环境，{{ stats.totalPipes }} 个渠道，{{ stats.totalConfigs }} 个配置
        <span v-if="stats.selectedConfigs > 0" class="text-primary ml-2">
          (已选择 {{ stats.selectedConfigs }} 个配置)
        </span>
      </div>
    </template>
  </div>
</template>
