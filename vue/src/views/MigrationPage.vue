<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { Layout } from '@/components/ui/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Lock } from 'lucide-vue-next'
import SourceTargetSelector from '@/components/Migration/SourceTargetSelector.vue'
import ChangePreview from '@/components/Migration/ChangePreview.vue'
import MigrationOperation from '@/components/Migration/MigrationOperation.vue'
import { useEnvironmentStore } from '@/stores/environment'
import { useFreezeStore } from '@/stores/freeze'
import { transferApi } from '@/lib/api/transfer'
import { configApi } from '@/lib/api/config'
import { fromApiConfig } from '@/lib/api/transformers'
import { isFreezeBlockedError, freezeErrorMessage } from '@/lib/api/error'
import { toast } from 'vue-sonner'
import type { ConfigItem, ConfigType } from '@/lib/types'

type MigrationStatus = 'idle' | 'previewing' | 'migrating' | 'success' | 'error'

export interface MigrationConfig {
  id: string
  name: string
  alias: string
  type: ConfigType
  sourceValue: string
  targetValue: string | null
  hasConflict: boolean
  isNew: boolean
}

const environmentStore = useEnvironmentStore()
const freezeStore = useFreezeStore()

const sourceEnvKey = ref('')
const sourcePipelineKey = ref('')
const targetEnvKey = ref('')
const targetPipelineKey = ref('')
const selectedConfigs = ref<string[]>([])
const status = ref<MigrationStatus>('idle')
const previewData = ref<MigrationConfig[]>([])
const overwriteConflicts = ref(false)
const sourceConfigs = ref<ConfigItem[]>([])

onMounted(() => {
  environmentStore.fetchOverview()
})

// 目标范围锁定状态:locked 时阻止启动迁移;源范围 locked 允许只读复制
watch([targetEnvKey, targetPipelineKey], ([envKey, pipelineKey]) => {
  if (envKey && pipelineKey) {
    freezeStore.loadStatus(envKey, pipelineKey)
  }
})

watch([sourceEnvKey, sourcePipelineKey], ([envKey, pipelineKey]) => {
  if (envKey && pipelineKey) {
    freezeStore.loadStatus(envKey, pipelineKey)
  }
})

const targetLocked = computed(() => freezeStore.isLocked(targetEnvKey.value, targetPipelineKey.value))
const targetFrozenReason = computed(() => freezeStore.statusFor(targetEnvKey.value, targetPipelineKey.value)?.last_reason || '')
const sourceLocked = computed(() => freezeStore.isLocked(sourceEnvKey.value, sourcePipelineKey.value))

const validatePreviewInputs = (): boolean => {
  return !!sourceEnvKey.value && !!sourcePipelineKey.value && !!targetEnvKey.value && !!targetPipelineKey.value
}

const fetchSourceConfigs = async (): Promise<ConfigItem[]> => {
  const sourceResponse = await configApi.list({
    environmentKey: sourceEnvKey.value,
    pipelineKey: sourcePipelineKey.value,
  })
  return (sourceResponse.list || []).map(fromApiConfig)
}

const fetchTargetConfigs = async (): Promise<Map<string, ConfigItem>> => {
  const targetResponse = await configApi.list({
    environmentKey: targetEnvKey.value,
    pipelineKey: targetPipelineKey.value,
  })
  const targetList = (targetResponse.list || []).map(fromApiConfig)
  return new Map(targetList.map((c: ConfigItem) => [c.name, c]))
}

const generatePreviewData = (sourceList: ConfigItem[], targetMap: Map<string, ConfigItem>): MigrationConfig[] => {
  return sourceList.map((config: ConfigItem) => {
    const targetConfig = targetMap.get(config.name)
    const hasConflict = targetConfig !== undefined && targetConfig.content !== config.content
    const isNew = targetConfig === undefined

    return {
      id: config.id,
      name: config.name,
      alias: config.alias,
      type: config.type,
      sourceValue: config.content,
      targetValue: targetConfig ? targetConfig.content : null,
      hasConflict,
      isNew,
    }
  })
}

const handlePreview = async () => {
  if (!validatePreviewInputs()) return
  if (targetLocked.value) {
    toast.error(`目标范围已冻结,无法迁移到该范围${targetFrozenReason.value ? `(原因:${targetFrozenReason.value})` : ''}`)
    return
  }

  status.value = 'previewing'

  try {
    const sourceList = await fetchSourceConfigs()
    sourceConfigs.value = sourceList

    const targetMap = await fetchTargetConfigs()
    const preview = generatePreviewData(sourceList, targetMap)

    previewData.value = preview
    selectedConfigs.value = preview.map((p) => p.id)
    status.value = 'idle'
  } catch (error) {
    status.value = 'error'
    toast.error('获取配置失败，请重试')
  }
}

const validateMigrationInputs = (): boolean => {
  if (selectedConfigs.value.length === 0) return false

  const hasConflicts = previewData.value.some((p) => p.hasConflict && selectedConfigs.value.includes(p.id))
  if (hasConflicts && !overwriteConflicts.value) {
    return false
  }

  return true
}

const getSelectedResourceKeys = (): string[] => {
  return sourceConfigs.value
    .filter((c: ConfigItem) => selectedConfigs.value.includes(c.id))
    .map((c: ConfigItem) => c.id)
}

const executeMigration = async (resourceKeys: string[]) => {
  return transferApi.migrate({
    sourceEnvironmentKey: sourceEnvKey.value,
    sourcePipelineKey: sourcePipelineKey.value,
    targetEnvironmentKey: targetEnvKey.value,
    targetPipelineKey: targetPipelineKey.value,
    resourceKeys: resourceKeys,
    overwrite: overwriteConflicts.value,
  })
}

interface MigrationResult {
  succeeded: number
  skipped: number
  items?: Array<{ status: string }>
}

const handleMigrationResult = (result: MigrationResult | null) => {
  if (result) {
    const { succeeded, skipped, items } = result
    const failed = items?.filter((item: { status: string }) => item.status === 'failed').length || 0

    if (failed === 0) {
      status.value = 'success'
      toast.success(`成功迁移 ${succeeded} 个配置${skipped > 0 ? `，跳过 ${skipped} 个` : ''}`)
    } else {
      status.value = 'error'
      toast.error(`部分配置迁移失败：成功 ${succeeded}，跳过 ${skipped}，失败 ${failed}`)
    }
  } else {
    status.value = 'error'
    toast.error('迁移失败，请重试')
  }
}

const handleMigrate = async () => {
  if (!validateMigrationInputs()) return
  if (targetLocked.value) {
    toast.error('目标范围已冻结,迁移被阻止;请先解除冻结')
    return
  }

  status.value = 'migrating'

  try {
    const selectedResourceKeys = getSelectedResourceKeys()
    const result = await executeMigration(selectedResourceKeys)
    handleMigrationResult(result)
  } catch (error) {
    status.value = 'error'
    if (isFreezeBlockedError(error)) {
      await freezeStore.loadStatus(targetEnvKey.value, targetPipelineKey.value)
      toast.error(freezeErrorMessage(error))
    } else {
      toast.error('迁移失败，请重试')
    }
  }
}

const handleReset = () => {
  sourceEnvKey.value = ''
  sourcePipelineKey.value = ''
  targetEnvKey.value = ''
  targetPipelineKey.value = ''
  selectedConfigs.value = []
  previewData.value = []
  sourceConfigs.value = []
  status.value = 'idle'
  overwriteConflicts.value = false
}
</script>

<template>
  <Layout
    title="配置迁移"
    description="在不同环境和渠道之间迁移配置项"
  >
    <MigrationOperation 
      v-if="status === 'success' || status === 'error'"
      :status="status" 
      :selected-configs="selectedConfigs" 
      @reset="handleReset"
    />

    <template v-else>
      <Alert v-if="targetLocked" variant="destructive" class="mb-4">
        <Lock class="h-4 w-4" />
        <AlertTitle>目标范围已冻结,迁移被阻止</AlertTitle>
        <AlertDescription>
          {{ targetEnvKey }}/{{ targetPipelineKey }}
          <span v-if="targetFrozenReason">(原因:{{ targetFrozenReason }})</span>
          。请先解除目标冻结后再迁移。
        </AlertDescription>
      </Alert>
      <Alert v-else-if="sourceLocked" class="mb-4">
        <Lock class="h-4 w-4" />
        <AlertTitle>源范围已冻结</AlertTitle>
        <AlertDescription>
          从冻结源复制到未冻结目标是允许的;源范围本身不会被修改。
        </AlertDescription>
      </Alert>

      <SourceTargetSelector 
        v-model:source-env-key="sourceEnvKey"
        v-model:source-pipeline-key="sourcePipelineKey"
        v-model:target-env-key="targetEnvKey"
        v-model:target-pipeline-key="targetPipelineKey"
        :status="status"
        :target-locked="targetLocked"
        :target-frozen-reason="targetFrozenReason"
        @preview="handlePreview"
      />

      <ChangePreview 
        :preview-data="previewData"
        v-model:selected-configs="selectedConfigs"
        v-model:overwrite-conflicts="overwriteConflicts"
        :status="status"
        @reset="handleReset"
        @migrate="handleMigrate"
      />
    </template>
  </Layout>
</template>
