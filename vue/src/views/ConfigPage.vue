<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useScopeSelection } from '@/composables/useScopeSelection'
import SpectrumLayout from '@/components/spectrum/SpectrumLayout.vue'
import { useEnvironmentStore } from '@/stores/environment'
import { useConfigStore } from '@/stores/config'
import { useFreezeStore } from '@/stores/freeze'
import { type ConfigType } from '@/lib/types'
import { resolveAssetUrl } from '@/lib/utils'
import { ApiError, isFreezeBlockedError, freezeErrorMessage } from '@/lib/api/error'
import EnvironmentSelector from '@/components/Config/EnvironmentSelector.vue'
import FreezePanel from '@/components/Config/FreezePanel.vue'
import ConfigList from '@/components/Config/ConfigList.vue'
import ConfigImagePreview from '@/components/Config/ConfigImagePreview.vue'

const environmentStore = useEnvironmentStore()
const configStore = useConfigStore()
const freezeStore = useFreezeStore()
onUnmounted(() => configStore.resetConfigs())

const { selectedEnvKey, selectedPipelineKey, selectedScopeId, initialize } = useScopeSelection(() => environmentStore.environments)
const imagePreviewUrl = ref<string | null>(null)
const imagePreviewName = ref<string>('')

onMounted(async () => {
  await environmentStore.fetchOverview()
  if (!environmentStore.error) initialize()
})

const selectedEnvironment = computed(() =>
  environmentStore.environments.find((env) => env.key === selectedEnvKey.value)
)

const pipelines = computed(() => selectedEnvironment.value?.pipelines || [])

const selectedPipeline = computed(() =>
  pipelines.value.find((pipeline) => pipeline.key === selectedPipelineKey.value)
)

const frozen = computed(() => freezeStore.isLocked(selectedEnvKey.value, selectedPipelineKey.value))
const frozenReason = computed(() => freezeStore.statusFor(selectedEnvKey.value, selectedPipelineKey.value)?.last_reason || '')

const refreshConfigs = () => {
  if (selectedEnvKey.value && selectedPipelineKey.value) {
    void configStore.fetchConfigs(selectedEnvKey.value, selectedPipelineKey.value)
  }
}

watch([selectedEnvKey, selectedPipelineKey, selectedScopeId], ([envKey, pipelineKey]) => {
  imagePreviewUrl.value = null
  if (envKey && pipelineKey) {
    configStore.fetchConfigs(envKey, pipelineKey)
  } else {
    configStore.resetConfigs()
  }
})

const handleCreate = async (config: {
  scopeId: string
  name: string
  alias: string
  type: string
  content: string
  description: string
}, done: (ok: boolean, message?: string, kind?: string) => void) => {
  const environmentKey = selectedEnvKey.value
  const pipelineKey = selectedPipelineKey.value
  if (!selectedEnvKey.value || !selectedPipelineKey.value) {
    done(false, '请先选择环境和渠道')
    return
  }
  try {
    await configStore.createConfig(environmentKey, pipelineKey, {
      name: config.name,
      alias: config.alias,
      type: config.type as ConfigType,
      content: config.content,
      description: config.description,
      scopeId: config.scopeId,
    })
    done(true)
  } catch (error) {
    if (isFreezeBlockedError(error)) {
      // 页面已打开后被锁定:保留草稿,刷新状态,不允许显示成功
      await freezeStore.loadStatus(environmentKey, pipelineKey)
      done(false, `${freezeErrorMessage(error)}。已刷新冻结状态`)
      return
    }
    done(false, error instanceof Error ? error.message : '保存失败', error instanceof ApiError ? error.errorType : '')
  }
}

const handleUpdate = async (config: {
  scopeId: string
  id: string
  expectedEditRevision: string
  name: string
  alias: string
  type: string
  content: string
  description: string
}, done: (ok: boolean, message?: string, kind?: string) => void) => {
  const environmentKey = selectedEnvKey.value
  const pipelineKey = selectedPipelineKey.value
  if (!selectedEnvKey.value || !selectedPipelineKey.value) {
    done(false, '请先选择环境和渠道')
    return
  }
  try {
    await configStore.updateConfig(environmentKey, pipelineKey, {
      id: config.id,
      expectedEditRevision: config.expectedEditRevision,
      scopeId: config.scopeId,
      name: config.name,
      alias: config.alias,
      type: config.type as ConfigType,
      content: config.content,
      description: config.description,
    })
    done(true)
  } catch (error) {
    if (isFreezeBlockedError(error)) {
      await freezeStore.loadStatus(environmentKey, pipelineKey)
      done(false, `${freezeErrorMessage(error)}。已刷新冻结状态`)
      return
    }
    const kind = error instanceof ApiError ? error.errorType || (error.code === -1 ? 'UNKNOWN_RESULT' : '') : 'UNKNOWN_RESULT'
    done(false, error instanceof Error ? error.message : '保存结果未确认，请重新加载核对', kind)
  }
}

const handleDelete = async (id: string, scopeId: string, done: (ok: boolean, message?: string) => void) => {
  const environmentKey = selectedEnvKey.value
  const pipelineKey = selectedPipelineKey.value
  if (!environmentKey || !pipelineKey) { done(false, '请先选择环境和渠道'); return }
  try {
    await configStore.deleteConfig(environmentKey, pipelineKey, id, scopeId)
    done(true)
  } catch (error) {
    if (isFreezeBlockedError(error)) {
      await freezeStore.loadStatus(environmentKey, pipelineKey)
    }
    done(false, isFreezeBlockedError(error) ? freezeErrorMessage(error) : error instanceof Error ? error.message : '删除失败')
  }
}

const handleImagePreview = (url: string, name: string = '图片预览') => {
  imagePreviewUrl.value = resolveAssetUrl(url)
  imagePreviewName.value = name
}

const handleCloseImagePreview = () => {
  imagePreviewUrl.value = null
}
</script>

<template>
  <SpectrumLayout>
    <div class="config-page-heading"><p class="spectrum-eyebrow">CONFIGURATION / EDITING AREA</p><h1>配置管理</h1><p>修改后需创建版本并切换生效。</p></div>
    <EnvironmentSelector
      v-model:selected-env-key="selectedEnvKey"
      v-model:selected-pipeline-key="selectedPipelineKey"
    />

    <FreezePanel
      :key="selectedScopeId"
      v-if="selectedEnvKey && selectedPipelineKey"
      :environment-key="selectedEnvKey"
      :pipeline-key="selectedPipelineKey"
      :environment-name="selectedEnvironment?.name"
      :pipeline-name="selectedPipeline?.name"
      class="mt-4"
    />

    <ConfigList
      :key="selectedScopeId"
      :scope-id="selectedScopeId"
      :configs="configStore.configs"
      :loading="configStore.loading"
      :error="configStore.error"
      :selected-env-key="selectedEnvKey"
      :selected-pipeline-key="selectedPipelineKey"
      :selected-environment-name="selectedEnvironment?.name"
      :selected-pipeline-name="selectedPipeline?.name"
      :frozen="frozen"
      :frozen-reason="frozenReason"
      @create="handleCreate"
      @update="handleUpdate"
      @delete="handleDelete"
      @image-preview="handleImagePreview"
      @retry="refreshConfigs"
    />

    <ConfigImagePreview
      :url="imagePreviewUrl"
      :name="imagePreviewName"
      @close="handleCloseImagePreview"
    />
  </SpectrumLayout>
</template>
