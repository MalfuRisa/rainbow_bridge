<script setup lang="ts">
import { computed } from 'vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Layers, GitBranch } from 'lucide-vue-next'
import { useEnvironmentStore } from '@/stores/environment'

const props = defineProps<{
  selectedEnvKey: string
  selectedPipelineKey: string
}>()

const emit = defineEmits<{
  (e: 'update:selectedEnvKey', value: string): void
  (e: 'update:selectedPipelineKey', value: string): void
}>()

const environmentStore = useEnvironmentStore()

const selectedEnvironment = computed(() =>
  environmentStore.environments.find((env) => env.key === props.selectedEnvKey)
)

const pipelines = computed(() => selectedEnvironment.value?.pipelines || [])

const handleEnvChange = (envKey: string) => {
  emit('update:selectedEnvKey', envKey)
  emit('update:selectedPipelineKey', '')
}

const handlePipelineChange = (pipelineKey: string) => {
  emit('update:selectedPipelineKey', pipelineKey)
}
</script>

<template>
  <Card class="config-scope-selector">
    <CardHeader class="config-scope-header">
      <CardTitle class="config-scope-title">工作范围</CardTitle>
    </CardHeader>
    <CardContent class="config-scope-content">
      <div class="config-scope-fields">
        <div class="flex items-center gap-3">
          <div class="flex items-center gap-2 text-muted-foreground">
            <Layers class="w-5 h-5" />
            <span class="text-sm font-medium">环境</span>
          </div>
          <Select 
            :model-value="selectedEnvKey" 
            @update:model-value="handleEnvChange" 
            :disabled="environmentStore.loading"
          >
            <SelectTrigger class="config-scope-trigger w-56 h-10" aria-label="环境">
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
            <GitBranch class="w-5 h-5" />
            <span class="text-sm font-medium">渠道</span>
          </div>
          <Select
            :model-value="selectedPipelineKey"
            @update:model-value="handlePipelineChange"
            :disabled="!selectedEnvKey"
          >
            <SelectTrigger class="config-scope-trigger w-56 h-10" aria-label="渠道">
              <SelectValue :placeholder="selectedEnvKey ? '请选择渠道' : '先选择环境'" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="pipeline in pipelines" :key="pipeline.key" :value="pipeline.key">
                {{ pipeline.name }} ({{ pipeline.key }})
              </SelectItem>
            </SelectContent>
          </Select>
        </div>

        <Badge v-if="selectedEnvKey && selectedPipelineKey" variant="secondary" class="config-scope-summary ml-auto px-3 py-1 text-sm">
          已选择: {{ selectedEnvironment?.name }} / {{ pipelines.find(p => p.key === selectedPipelineKey)?.name }}
        </Badge>
      </div>
      <div v-if="environmentStore.error" class="config-environment-error" role="alert">{{ environmentStore.error }}<button class="spectrum-text-button" @click="environmentStore.fetchOverview()">重新加载环境</button></div>
    </CardContent>
  </Card>
</template>
