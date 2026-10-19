<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { Layout } from '@/components/ui/layout'
import { useEnvironmentStore } from '@/stores/environment'
import { useConfigStore } from '@/stores/config'
import { useFreezeStore } from '@/stores/freeze'
import { type ConfigType } from '@/lib/types'
import { resolveAssetUrl } from '@/lib/utils'
import { isFreezeBlockedError, freezeErrorMessage } from '@/lib/api/error'
import EnvironmentSelector from '@/components/Config/EnvironmentSelector.vue'
import FreezePanel from '@/components/Config/FreezePanel.vue'
import ConfigList from '@/components/Config/ConfigList.vue'
import ImagePreview from '@/components/resources/ImagePreview.vue'

const environmentStore = useEnvironmentStore()
const configStore = useConfigStore()
const freezeStore = useFreezeStore()

const selectedEnvKey = ref('')
const selectedPipelineKey = ref('')
const imagePreviewUrl = ref<string | null>(null)
const imagePreviewName = ref<string>('')

onMounted(() => {
  environmentStore.fetchOverview()
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
})

watch([selectedEnvKey, selectedPipelineKey], ([envKey, pipelineKey]) => {
  if (envKey && pipelineKey) {
    configStore.fetchConfigs(envKey, pipelineKey)
    freezeStore.loadStatus(envKey, pipelineKey)
  }
})

const handleCreate = async (config: {
  name: string
  alias: string
  type: string
  content: string
  description: string
}, done: (ok: boolean, message?: string) => void) => {
  if (!selectedEnvKey.value || !selectedPipelineKey.value) {
    done(false, '请先选择环境和渠道')
    return
  }
  try {
    await configStore.createConfig(selectedEnvKey.value, selectedPipelineKey.value, {
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
      await freezeStore.loadStatus(selectedEnvKey.value, selectedPipelineKey.value)
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
  if (!selectedEnvKey.value || !selectedPipelineKey.value) {
    done(false, '请先选择环境和渠道')
    return
  }
  try {
    await configStore.updateConfig(selectedEnvKey.value, selectedPipelineKey.value, {
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
      await freezeStore.loadStatus(selectedEnvKey.value, selectedPipelineKey.value)
      done(false, `${freezeErrorMessage(error)}。已刷新冻结状态`)
      return
    }
    done(false, error instanceof Error ? error.message : '保存失败')
  }
}

const handleDelete = async (id: string) => {
  if (!selectedEnvKey.value || !selectedPipelineKey.value) return
  try {
    await configStore.deleteConfig(selectedEnvKey.value, selectedPipelineKey.value, id)
  } catch (error) {
    if (isFreezeBlockedError(error)) {
      await freezeStore.loadStatus(selectedEnvKey.value, selectedPipelineKey.value)
    }
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
  <Layout
    title="配置管理"
    description="管理指定环境和渠道的运行时配置项"
  >
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
      :configs="configStore.configs"
      :loading="configStore.loading"
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
    />

    <ImagePreview
      :is-open="!!imagePreviewUrl"
      :image-url="imagePreviewUrl || ''"
      :image-name="imagePreviewName"
      :on-close="handleCloseImagePreview"
    />
  </Layout>
</template>
