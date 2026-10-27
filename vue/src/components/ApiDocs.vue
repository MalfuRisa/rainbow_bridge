<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Copy, Check, ExternalLink } from 'lucide-vue-next'
import { toast } from 'vue-sonner'
import { getBasePath } from '@/lib/api/client'

const copiedIndex = ref<number | null>(null)
let copiedTimer: ReturnType<typeof setTimeout> | undefined
onUnmounted(() => clearTimeout(copiedTimer))

/** 复制的是真实请求路径(带部署前缀),避免漏前缀或前缀重复。 */
const basePath = computed(() => getBasePath().replace(/\/$/, ''))

const apiEndpoints = computed(() => [
  {
    method: 'GET',
    path: `${basePath.value}/api/v1/runtime/release`,
    title: '读取生效配置',
    description: '获取当前环境与渠道的生效配置，跟随版本切换更新。',
  },
  {
    method: 'GET',
    path: `${basePath.value}/api/v1/runtime/release?version=<版本号>`,
    title: '读取指定版本',
    description: '按版本号获取对应配置，其他版本的生效切换不会改变读取目标。',
  },
  {
    method: 'GET',
    path: `${basePath.value}/api/v1/runtime/release/static?version=<版本号>&expected_snapshot_id=<UUID>`,
    title: '导出版本资源包',
    description: '下载指定版本的配置与配套资源，打包为 ZIP 文件。',
  },
])

const copyPath = async (path: string, index: number) => {
  try { await navigator.clipboard.writeText(path) }
  catch { toast.error('复制失败，请手动选择路径复制'); return }
  clearTimeout(copiedTimer)
  copiedIndex.value = index
  toast.success('已复制接口路径')
  copiedTimer = setTimeout(() => {
    copiedIndex.value = null
  }, 2000)
}
</script>

<template>
  <!-- §2.1 / §3.6: capability and purpose first; method/path and copy are secondary.
       Reuse spectrum text, spacing and semantic colors without adding API actions. -->
  <Card class="spectrum-api-panel">
    <CardHeader>
      <div class="spectrum-api-header">
        <div class="min-w-0">
          <CardTitle class="text-lg">API 接入</CardTitle>
          <p class="text-sm text-muted-foreground mt-1">
            业务系统可通过 API 获取配置、指定版本或下载资源包。
          </p>
        </div>
        <a
          href="https://github.com/MalfuRisa/rainbow_bridge/blob/main_v1/docs/docs/api/index.md"
          target="_blank"
          rel="noopener noreferrer"
          class="spectrum-api-document-link text-sm text-muted-foreground hover:text-primary flex items-center gap-1"
        >
          查看完整 API
          <ExternalLink class="w-3 h-3" />
        </a>
      </div>
    </CardHeader>
    <CardContent class="space-y-2">
      <div
        v-for="(endpoint, index) in apiEndpoints"
        :key="endpoint.path"
        class="spectrum-api-row"
      >
        <div class="min-w-0">
          <h3 class="spectrum-api-title">{{ endpoint.title }}</h3>
          <p class="spectrum-api-description">{{ endpoint.description }}</p>
          <div class="spectrum-api-path">
            <Badge class="spectrum-api-method font-mono text-xs shrink-0">{{ endpoint.method }}</Badge>
            <code>{{ endpoint.path }}</code>
          </div>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <Button variant="ghost" size="icon" class="h-10 w-10" :aria-label="`复制接口路径 ${endpoint.title}`" @click="copyPath(endpoint.path, index)">
            <Check v-if="copiedIndex === index" class="w-4 h-4 text-green-600" />
            <Copy v-else class="w-4 h-4" />
          </Button>
        </div>
      </div>
    </CardContent>
  </Card>
</template>

<style scoped>
.spectrum-api-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 24px; }
.spectrum-api-document-link { flex-shrink: 0; white-space: nowrap; }
.spectrum-api-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 24px; padding: 24px 0; }
.spectrum-api-row:last-child { border-bottom: 0; }
.spectrum-api-title { font-size: 16px; line-height: 1.5; }
.spectrum-api-description { margin-top: 4px; }
.spectrum-api-path { margin-top: 12px; }
.spectrum-api-path code { min-width: 0; user-select: text; }
</style>
