<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, computed } from 'vue'
import { useRoute } from 'vue-router'
import SpectrumLayout from '@/components/spectrum/SpectrumLayout.vue'
import SpectrumPageHeader from '@/components/spectrum/SpectrumPageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Loader2, Layers, LockKeyhole } from 'lucide-vue-next'
import { toast } from 'vue-sonner'
import { useEnvironmentStore } from '@/stores/environment'
import { useAssetStore } from '@/stores/asset'
import { useFreezeStore } from '@/stores/freeze'
import { resolveAssetUrl } from '@/lib/utils'

// 导入拆分的组件
import EnvironmentChannelSelector from '@/components/resources/EnvironmentChannelSelector.vue'
import ResourceList from '@/components/resources/ResourceList.vue'
import ImagePreview from '@/components/resources/ImagePreview.vue'
import Uploader from '@/components/resources/Uploader.vue'

const environmentStore = useEnvironmentStore()
const assetStore = useAssetStore()
const freezeStore = useFreezeStore()
const route = useRoute()

const searchTerm = ref('')
const selectedEnvKey = ref('')
const selectedPipelineKey = ref('')
const copiedId = ref<string | null>(null)

const envPipelineModel = ref({
  envKey: selectedEnvKey.value,
  pipelineKey: selectedPipelineKey.value
})

watch(envPipelineModel, (newValue) => {
  selectedEnvKey.value = newValue.envKey
  selectedPipelineKey.value = newValue.pipelineKey
}, { deep: true })

// 预览相关状态
const previewImage = ref<string>('')
const previewImageName = ref<string>('')
const isPreviewOpen = ref(false)

onMounted(async () => {
  await environmentStore.fetchOverview()
  const env = environmentStore.environments.find(item => item.key === route.query.env)
  if (env?.pipelines.some(item => item.key === route.query.pipeline)) {
    envPipelineModel.value = { envKey: env.key, pipelineKey: String(route.query.pipeline) }
  }
})
onUnmounted(() => assetStore.resetAssets())

// 监听环境和渠道变化
watch([selectedEnvKey, selectedPipelineKey], ([envKey, pipelineKey]) => {
  if (envKey && pipelineKey) {
    assetStore.fetchAssets(envKey, pipelineKey)
    freezeStore.loadStatus(envKey, pipelineKey)
  } else {
    assetStore.resetAssets()
  }
  searchTerm.value = ''
  copiedId.value = null
})

const frozen = computed(() => freezeStore.isLocked(selectedEnvKey.value, selectedPipelineKey.value))
const frozenReason = computed(() => freezeStore.statusFor(selectedEnvKey.value, selectedPipelineKey.value)?.last_reason || '')

// 处理复制链接
const handleCopy = async (url: string, id: string) => {
  const fullUrl = url.startsWith('http') ? url : `${window.location.origin}${url}`
  try { await navigator.clipboard.writeText(fullUrl) }
  catch { toast.error('复制失败，请手动复制资源链接'); return }
  copiedId.value = id
  setTimeout(() => copiedId.value = null, 2000)
}

// 处理图片预览
const handlePreviewImage = (url: string, name: string) => {
  previewImage.value = resolveAssetUrl(url)
  previewImageName.value = name
  isPreviewOpen.value = true
}

// 关闭预览
const closePreview = () => {
  isPreviewOpen.value = false
}

// 处理搜索变化
const handleSearchChange = (value: string) => {
  searchTerm.value = value
}
</script>

<template>
  <SpectrumLayout>
    <div class="spectrum-operations spectrum-resources-page">
    <SpectrumPageHeader eyebrow="ASSETS / CONTENT LIBRARY" title="资源管理" />
    <!-- 环境和渠道选择组件 -->
    <Card class="spectrum-panel spectrum-scope-panel">
      <CardHeader class="spectrum-panel-header">
        <CardTitle class="text-lg">工作范围</CardTitle>
      </CardHeader>
      <CardContent class="spectrum-panel-content">
        <EnvironmentChannelSelector
          v-model="envPipelineModel"
        />
      </CardContent>
    </Card>
    <div v-if="frozen" class="spectrum-resource-freeze"><LockKeyhole :size="16" /><strong>已冻结 · 资源只读</strong><span>{{ frozenReason || '当前范围不可上传资源，预览与链接复制不受影响。' }}</span></div>

    <!-- 资源列表和上传功能 -->
    <Card class="spectrum-panel spectrum-section">
      <CardHeader class="spectrum-panel-header">
        <div class="spectrum-resource-heading">
          <CardTitle class="text-lg">资源列表 <span v-if="selectedEnvKey && selectedPipelineKey && !assetStore.loading" class="ml-3 text-xs text-muted-foreground font-normal">{{ assetStore.assets.length }} 份资源</span></CardTitle>
          <!-- 上传功能组件 -->
          <Uploader
            :selectedEnvKey="selectedEnvKey"
            :selectedPipelineKey="selectedPipelineKey"
            :searchTerm="searchTerm"
            :onSearchChange="handleSearchChange"
            :frozen="frozen"
            :frozenReason="frozenReason"
          />
        </div>
      </CardHeader>
      <CardContent class="spectrum-panel-content">
        <div v-if="!selectedEnvKey || !selectedPipelineKey" class="spectrum-empty">
          <div class="w-16 h-16 rounded-full bg-muted flex items-center justify-center mb-4">
            <Layers class="w-8 h-8 text-muted-foreground" />
          </div>
          <h3 class="text-lg font-medium mb-2">请先选择环境和渠道</h3>
        </div>
        <div v-else-if="assetStore.loading" class="flex items-center justify-center py-16">
          <Loader2 class="w-8 h-8 animate-spin text-muted-foreground" />
        </div>
        <div v-else-if="assetStore.error" class="spectrum-empty" role="alert"><h3>资源加载失败</h3><p>{{ assetStore.error }}</p><Button variant="outline" @click="assetStore.fetchAssets(selectedEnvKey, selectedPipelineKey)">重试</Button></div>
        <div v-else>
          <!-- 资源列表组件 -->
          <ResourceList
            :selectedEnvKey="selectedEnvKey"
            :selectedPipelineKey="selectedPipelineKey"
            :onPreviewImage="handlePreviewImage"
            :onCopy="handleCopy"
            :copiedId="copiedId"
            :searchTerm="searchTerm"
          />
        </div>
      </CardContent>
    </Card>

    <!-- 图片预览组件 -->
    <ImagePreview
      :isOpen="isPreviewOpen"
      :imageUrl="previewImage"
      :imageName="previewImageName"
      :onClose="closePreview"
    />
    </div>
  </SpectrumLayout>
</template>
