<script setup lang="ts">
import { ref } from 'vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Upload, Loader2, Search } from 'lucide-vue-next'
import { useAssetStore } from '@/stores/asset'
import { useFreezeStore } from '@/stores/freeze'
import { isFreezeBlockedError } from '@/lib/api/error'

const assetStore = useAssetStore()
const freezeStore = useFreezeStore()
const uploading = ref(false)

const props = defineProps<{
  selectedEnvKey: string
  selectedPipelineKey: string
  searchTerm: string
  onSearchChange: (value: string) => void
  /** 当前范围已冻结:上传入口禁用 */
  frozen?: boolean
  frozenReason?: string
}>()

const fileInputRef = ref<HTMLInputElement | null>(null)

const handleUploadClick = () => {
  if (props.frozen) {
    toast.error(`该范围已冻结,无法上传资源${props.frozenReason ? `(原因:${props.frozenReason})` : ''}`)
    return
  }
  fileInputRef.value?.click()
}

const handleFileChange = async (e: Event) => {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file || !props.selectedEnvKey || !props.selectedPipelineKey) return
  if (uploading.value) return
  if (props.frozen) {
    toast.error('该范围已冻结,无法上传资源')
    return
  }

  const env = props.selectedEnvKey
  const pipeline = props.selectedPipelineKey
  uploading.value = true
  try {
    await assetStore.uploadAsset(file, env, pipeline)
  } catch (error) {
    if (isFreezeBlockedError(error)) await freezeStore.loadStatus(env, pipeline)
  } finally {
    uploading.value = false
    if (fileInputRef.value) fileInputRef.value.value = ''
  }
}
</script>

<template>
  <div class="spectrum-uploader">
    <div class="spectrum-resource-search">
      <Search class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
      <Input
        placeholder="搜索资源..."
        aria-label="搜索资源名称"
        class="pl-9 w-64"
        :value="searchTerm"
        @input="onSearchChange($event.target.value)"
        :disabled="!selectedEnvKey || !selectedPipelineKey"
      />
    </div>
    <input
      type="file"
      ref="fileInputRef"
      class="hidden"
      @change="handleFileChange"
      accept="image/*,application/json,text/*"
    />
    <Button
      @click="handleUploadClick"
      :disabled="!selectedEnvKey || !selectedPipelineKey || assetStore.loading || uploading || frozen"
      :title="frozen ? `已冻结:${frozenReason || '该范围不可上传资源'}` : undefined"
    >
      <Loader2 v-if="uploading" class="w-4 h-4 mr-2 animate-spin" />
      <Upload v-else class="w-4 h-4 mr-2" />
      {{ uploading ? '上传中…' : '上传资源' }}
    </Button>
  </div>
</template>
