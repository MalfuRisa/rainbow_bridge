<script setup lang="ts">
import { computed } from 'vue'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { Button } from '@/components/ui/button'
import { ArrowRight, Loader2 } from 'lucide-vue-next'
import { useEnvironmentStore } from '@/stores/environment'
import type { Environment } from '@/lib/types'

interface Props {
  sourceEnvKey: string
  sourcePipelineKey: string
  targetEnvKey: string
  targetPipelineKey: string
  status: string
  /** 目标范围已冻结:阻止启动迁移(服务端同样会拒绝) */
  targetLocked?: boolean
  targetFrozenReason?: string
}

interface Emits {
  (e: 'update:sourceEnvKey', value: string): void
  (e: 'update:sourcePipelineKey', value: string): void
  (e: 'update:targetEnvKey', value: string): void
  (e: 'update:targetPipelineKey', value: string): void
  (e: 'preview'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const environmentStore = useEnvironmentStore()

const sourceEnvironment = computed(() =>
  environmentStore.environments.find((e: Environment) => e.key === props.sourceEnvKey)
)

const sourcePipelines = computed(() => sourceEnvironment.value?.pipelines || [])

const targetEnvironment = computed(() =>
  environmentStore.environments.find((e: Environment) => e.key === props.targetEnvKey)
)

const targetPipelines = computed(() => targetEnvironment.value?.pipelines || [])

const handleSourceEnvChange = (v: string) => {
  emit('update:sourceEnvKey', v)
  emit('update:sourcePipelineKey', '')
}

const handleTargetEnvChange = (v: string) => {
  emit('update:targetEnvKey', v)
  emit('update:targetPipelineKey', '')
}

const handleSourcePipelineChange = (v: string) => {
  emit('update:sourcePipelineKey', v)
}

const handleTargetPipelineChange = (v: string) => {
  emit('update:targetPipelineKey', v)
}

const handlePreview = () => {
  emit('preview')
}
</script>

<template>
  <Card class="spectrum-panel">
    <CardHeader class="spectrum-panel-header">
      <CardTitle class="text-base">选择源和目标</CardTitle>
      <CardDescription>
        选择要迁移配置的源环境/渠道和目标环境/渠道
      </CardDescription>
    </CardHeader>
    <CardContent class="spectrum-panel-content">
      <div class="spectrum-migration-route">
        <div class="spectrum-migration-source spectrum-migration-endpoint">
          <p class="spectrum-eyebrow">SOURCE / 复制来源</p><h2>源</h2>
          <div class="spectrum-migration-fields">
            <Select 
              :model-value="sourceEnvKey" 
              @update:model-value="handleSourceEnvChange" 
              :disabled="environmentStore.loading || status === 'migrating'"
            >
              <SelectTrigger aria-label="源环境">
                <SelectValue :placeholder="environmentStore.loading ? '加载中...' : '选择环境'" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="env in environmentStore.environments" :key="env.key" :value="env.key">
                  {{ env.name }}
                </SelectItem>
              </SelectContent>
            </Select>
            <Select 
              :model-value="sourcePipelineKey" 
              @update:model-value="handleSourcePipelineChange" 
              :disabled="!sourceEnvKey || status === 'migrating'"
            >
              <SelectTrigger aria-label="源渠道">
                <SelectValue placeholder="选择渠道" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="pipeline in sourcePipelines" :key="pipeline.key" :value="pipeline.key">
                  {{ pipeline.name }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        <div class="spectrum-migration-arrow" aria-hidden="true">
          <ArrowRight class="w-5 h-5 text-muted-foreground" />
        </div>

        <div class="spectrum-migration-target spectrum-migration-endpoint">
          <p class="spectrum-eyebrow">DESTINATION / 写入目标</p><h2>目标</h2>
          <div class="spectrum-migration-fields">
            <Select 
              :model-value="targetEnvKey" 
              @update:model-value="handleTargetEnvChange" 
              :disabled="environmentStore.loading || status === 'migrating'"
            >
              <SelectTrigger aria-label="目标环境">
                <SelectValue :placeholder="environmentStore.loading ? '加载中...' : '选择环境'" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="env in environmentStore.environments" :key="env.key" :value="env.key">
                  {{ env.name }}
                </SelectItem>
              </SelectContent>
            </Select>
            <Select 
              :model-value="targetPipelineKey" 
              @update:model-value="handleTargetPipelineChange" 
              :disabled="!targetEnvKey || status === 'migrating'"
            >
              <SelectTrigger aria-label="目标渠道">
                <SelectValue placeholder="选择渠道" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="pipeline in targetPipelines" :key="pipeline.key" :value="pipeline.key">
                  {{ pipeline.name }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
      </div>

      <div class="mt-6 flex justify-end">
        <Button
          @click="handlePreview"
          :disabled="!sourceEnvKey || !sourcePipelineKey || !targetEnvKey || !targetPipelineKey || status === 'previewing' || status === 'migrating' || targetLocked"
          :title="targetLocked ? `目标已冻结:${targetFrozenReason || '不可迁移到该范围'}` : undefined"
        >
          <Loader2 v-if="status === 'previewing'" class="w-4 h-4 mr-2 animate-spin" />
          {{ status === 'previewing' ? '预览中...' : '预览变更' }}
        </Button>
      </div>
    </CardContent>
  </Card>
</template>
