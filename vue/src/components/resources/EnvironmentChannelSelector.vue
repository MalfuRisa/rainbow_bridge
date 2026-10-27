<script setup lang="ts">
import { computed } from 'vue'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { Layers, GitBranch } from 'lucide-vue-next'
import { useEnvironmentStore } from '@/stores/environment'

const environmentStore = useEnvironmentStore()

const props = defineProps<{
  disabled?: boolean
  modelValue: {
    envKey: string
    pipelineKey: string
  }
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: { envKey: string; pipelineKey: string }): void
}>()

const selectedEnvKey = computed(() => props.modelValue.envKey)
const selectedPipelineKey = computed(() => props.modelValue.pipelineKey)

const selectedEnvironment = computed(() =>
  environmentStore.environments.find((e) => e.key === selectedEnvKey.value)
)

const pipelines = computed(() => selectedEnvironment.value?.pipelines || [])

const handleEnvChange = (envKey: string) => {
  emit('update:modelValue', { envKey, pipelineKey: '' })
}
</script>

<template>
  <div class="spectrum-shared-scope">
    <div class="flex items-center gap-3">
      <div class="flex items-center gap-2 text-muted-foreground">
        <Layers class="w-4 h-4" />
        <span class="text-sm font-medium">环境</span>
      </div>
      <Select :model-value="selectedEnvKey" @update:model-value="handleEnvChange" :disabled="disabled || environmentStore.loading">
        <SelectTrigger class="w-48" aria-label="环境">
          <SelectValue :placeholder="environmentStore.loading ? '加载中...' : '请选择环境'" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="env in environmentStore.environments" :key="env.key" :value="env.key">
            {{ env.name }} ({{ env.key }})
          </SelectItem>
        </SelectContent>
      </Select>
    </div>

    <div class="flex items-center gap-3">
      <div class="flex items-center gap-2 text-muted-foreground">
        <GitBranch class="w-4 h-4" />
        <span class="text-sm font-medium">渠道</span>
      </div>
      <Select
        :model-value="selectedPipelineKey"
        @update:model-value="emit('update:modelValue', { envKey: selectedEnvKey, pipelineKey: $event })"
        :disabled="disabled || environmentStore.loading || !selectedEnvKey"
      >
        <SelectTrigger class="w-48" aria-label="渠道">
          <SelectValue :placeholder="selectedEnvKey ? '请选择渠道' : '先选择环境'" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="pipeline in pipelines" :key="pipeline.key" :value="pipeline.key">
            {{ pipeline.name }} ({{ pipeline.key }})
          </SelectItem>
        </SelectContent>
      </Select>
    </div>

  </div>
</template>
