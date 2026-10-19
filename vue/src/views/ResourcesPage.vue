<script setup lang="ts">
import { ref, watch, onMounted, computed } from 'vue'
import { Layout } from '@/components/ui/layout'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Loader2, Layers } from 'lucide-vue-next'
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

onMounted(() => {
  environmentStore.fetchOverview()
})

// 监听环境和渠道变化
watch([selectedEnvKey, selectedPipelineKey], ([envKey, pipelineKey]) => {
  if (envKey && pipelineKey) {
    assetStore.fetchAssets(envKey, pipelineKey)
    freezeStore.loadStatus(envKey, pipelineKey)
  }
})

const frozen = computed(() => freezeStore.isLocked(selectedEnvKey.value, selectedPipelineKey.value))
const frozenReason = computed(() => freezeStore.statusFor(selectedEnvKey.value, selectedPipelineKey.value)?.last_reason || '')

// 处理复制链接
const handleCopy = async (url: string, id: string) => {
  const fullUrl = url.startsWith('http') ? url : `${window.location.origin}${url}`
  await navigator.clipboard.writeText(fullUrl)
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
  <Layout
    title="资源管理"
    description="集中管理静态资源，自动生成 URL"
  >
    <!-- 环境和渠道选择组件 -->
    <Card>
      <CardHeader>
        <CardTitle class="text-lg">选择环境与渠道</CardTitle>
      </CardHeader>
      <CardContent>
        <EnvironmentChannelSelector
          v-model="envPipelineModel"
        />
      </CardContent>
    </Card>

    <!-- 资源列表和上传功能 -->
    <Card>
      <CardHeader>
        <div class="flex items-center justify-between">
          <CardTitle class="text-lg">资源列表</CardTitle>
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
      <CardContent>
        <div v-if="!selectedEnvKey || !selectedPipelineKey" class="flex flex-col items-center justify-center py-16 text-center">
          <div class="w-16 h-16 rounded-full bg-muted flex items-center justify-center mb-4">
            <Layers class="w-8 h-8 text-muted-foreground" />
          </div>
          <h3 class="text-lg font-medium mb-2">请先选择环境和渠道</h3>
          <p class="text-muted-foreground text-sm max-w-sm">
            资源与环境和渠道关联，请在上方选择要管理的环境和渠道后查看对应的资源列表
          </p>
        </div>
        <div v-else-if="assetStore.loading" class="flex items-center justify-center py-16">
          <Loader2 class="w-8 h-8 animate-spin text-muted-foreground" />
        </div>
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
  </Layout>
</template>
