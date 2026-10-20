<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import SpectrumLayout from '@/components/spectrum/SpectrumLayout.vue'
import { useEnvironmentStore } from '@/stores/environment'
import { useConfigStore } from '@/stores/config'
import { useFreezeStore } from '@/stores/freeze'
import { type ConfigType } from '@/lib/types'
import { resolveAssetUrl } from '@/lib/utils'
import { isFreezeBlockedError, freezeErrorMessage } from '@/lib/api/error'
import EnvironmentSelector from '@/components/Config/EnvironmentSelector.vue'
import FreezePanel from '@/components/Config/FreezePanel.vue'
import ConfigList from '@/components/Config/ConfigList.vue'
import ConfigImagePreview from '@/components/Config/ConfigImagePreview.vue'

const environmentStore = useEnvironmentStore()
const configStore = useConfigStore()
const freezeStore = useFreezeStore()
const route = useRoute()
onUnmounted(() => configStore.resetConfigs())

const selectedEnvKey = ref('')
const selectedPipelineKey = ref('')
const imagePreviewUrl = ref<string | null>(null)
const imagePreviewName = ref<string>('')

onMounted(async () => {
  await environmentStore.fetchOverview()
  // Honor scoped links only after checking they identify an existing scope.
  const env = environmentStore.environments.find(item => item.key === route.query.env)
  const pipeline = env?.pipelines.find(item => item.key === route.query.pipeline)
  if (env && pipeline) {
    selectedEnvKey.value = env.key
    selectedPipelineKey.value = pipeline.key
  }
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

watch(selectedEnvKey, () => {
  selectedPipelineKey.value = ''
}, { flush: 'sync' })

const refreshConfigs = () => {
  if (selectedEnvKey.value && selectedPipelineKey.value) {
    void configStore.fetchConfigs(selectedEnvKey.value, selectedPipelineKey.value)
  }
}

watch([selectedEnvKey, selectedPipelineKey], ([envKey, pipelineKey]) => {
  if (envKey && pipelineKey) {
    configStore.fetchConfigs(envKey, pipelineKey)
  } else {
    configStore.resetConfigs()
  }
})

const handleCreate = async (config: {
  name: string
  alias: string
  type: string
  content: string
  description: string
}, done: (ok: boolean, message?: string) => void) => {
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
    })
    done(true)
  } catch (error) {
    if (isFreezeBlockedError(error)) {
      // 页面已打开后被锁定:保留草稿,刷新状态,不允许显示成功
      await freezeStore.loadStatus(environmentKey, pipelineKey)
      done(false, `${freezeErrorMessage(error)}。已刷新冻结状态`)
      return
    }
    done(false, error instanceof Error ? error.message : '保存失败')
  }
}

const handleUpdate = async (config: {
  id: string
  name: string
  alias: string
  type: string
  content: string
  description: string
}, done: (ok: boolean, message?: string) => void) => {
  const environmentKey = selectedEnvKey.value
  const pipelineKey = selectedPipelineKey.value
  if (!selectedEnvKey.value || !selectedPipelineKey.value) {
    done(false, '请先选择环境和渠道')
    return
  }
  try {
    await configStore.updateConfig(environmentKey, pipelineKey, {
      id: config.id,
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
    done(false, error instanceof Error ? error.message : '保存失败')
  }
}

const handleDelete = async (id: string, done: (ok: boolean, message?: string) => void) => {
  const environmentKey = selectedEnvKey.value
  const pipelineKey = selectedPipelineKey.value
  if (!environmentKey || !pipelineKey) { done(false, '请先选择环境和渠道'); return }
  try {
    await configStore.deleteConfig(environmentKey, pipelineKey, id)
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
    <div class="config-page-heading"><p class="spectrum-eyebrow">CONFIGURATION / EDITING AREA</p><h1>配置管理</h1><p>一处维护内容，按版本决定何时生效。</p></div>
    <EnvironmentSelector
      v-model:selected-env-key="selectedEnvKey"
      v-model:selected-pipeline-key="selectedPipelineKey"
    />

    <FreezePanel
      v-if="selectedEnvKey && selectedPipelineKey"
      :environment-key="selectedEnvKey"
      :pipeline-key="selectedPipelineKey"
      :environment-name="selectedEnvironment?.name"
      :pipeline-name="selectedPipeline?.name"
      class="mt-4"
    />

    <ConfigList
      :key="`${selectedEnvKey}/${selectedPipelineKey}`"
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
