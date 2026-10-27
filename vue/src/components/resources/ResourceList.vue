<script setup lang="ts">
import { computed, ref } from 'vue'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Eye, Copy, Check, FileImage, FileJson, FileText, File, ImageOff } from 'lucide-vue-next'
import { useAssetStore } from '@/stores/asset'
import { resolveAssetUrl } from '@/lib/utils'

const assetStore = useAssetStore()
const failedImages = ref(new Set<string>())
const props = defineProps<{
  selectedEnvKey: string
  selectedPipelineKey: string
  onPreviewImage: (url: string, name: string) => void
  onCopy: (url: string, id: string) => void
  copiedId: string | null
  searchTerm: string
}>()
const filteredAssets = computed(() => assetStore.assets.filter(resource => resource.name.toLowerCase().includes(props.searchTerm.toLowerCase())))

function getFileIcon(type: string) {
  if (type.startsWith('image/')) return FileImage
  if (type.includes('json')) return FileJson
  if (type.includes('text')) return FileText
  return File
}

function formatFileSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  const index = Math.min(3, Math.max(0, Math.floor(Math.log(bytes) / Math.log(1024))))
  return `${parseFloat((bytes / Math.pow(1024, index)).toFixed(1))} ${['B', 'KB', 'MB', 'GB'][index]}`
}
</script>

<template>
  <!-- §2.1: recognizable thumbnail + name first, technical URL second, actions last. -->
  <div class="spectrum-table">
    <Table class="resource-table">
      <TableHeader>
        <TableRow>
          <TableHead class="w-24">预览</TableHead>
          <TableHead>文件名</TableHead>
          <TableHead class="w-24">类型</TableHead>
          <TableHead class="w-24">大小</TableHead>
          <TableHead>URL</TableHead>
          <TableHead class="w-24 text-right">操作</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-if="filteredAssets.length === 0">
          <TableCell colspan="6" class="text-center py-8 text-muted-foreground">
            {{ searchTerm ? '未找到匹配的资源' : '当前环境和渠道下暂无资源' }}
          </TableCell>
        </TableRow>
        <TableRow v-for="resource in filteredAssets" :key="resource.id">
          <TableCell>
            <button v-if="resource.type.startsWith('image/')" class="resource-preview-button" :aria-label="`预览 ${resource.name}`" @click="onPreviewImage(resource.url, resource.name)">
              <img v-if="!failedImages.has(resource.id)" :src="resolveAssetUrl(resource.url)" :alt="resource.name" loading="lazy" @error="failedImages.add(resource.id)">
              <ImageOff v-else :size="24" />
            </button>
            <div v-else class="resource-preview-button"><component :is="getFileIcon(resource.type)" :size="24" /></div>
          </TableCell>
          <TableCell>
            <div class="resource-filename flex items-center gap-2">
              <component :is="getFileIcon(resource.type)" class="w-4 h-4 shrink-0 text-muted-foreground" />
              <span class="font-medium text-sm">{{ resource.name }}</span>
            </div>
          </TableCell>
          <TableCell><Badge variant="secondary" class="text-xs">{{ resource.type.split('/')[1] || resource.type }}</Badge></TableCell>
          <TableCell class="text-muted-foreground text-sm">{{ formatFileSize(resource.size) }}</TableCell>
          <TableCell><code class="resource-url" :title="resource.url">{{ resource.url }}</code></TableCell>
          <TableCell class="text-right">
            <div class="flex items-center justify-end gap-1">
              <Button v-if="resource.type.startsWith('image/')" variant="ghost" size="icon" title="预览图片" :aria-label="`查看 ${resource.name} 大图`" @click="onPreviewImage(resource.url, resource.name)"><Eye :size="16" /></Button>
              <Button variant="ghost" size="icon" title="复制链接" :aria-label="`复制 ${resource.name} 链接`" @click="onCopy(resource.url, resource.id)">
                <Check v-if="copiedId === resource.id" class="w-4 h-4" /><Copy v-else :size="16" />
              </Button>
            </div>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </div>
</template>
