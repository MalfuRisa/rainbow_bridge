<script setup lang="ts">
import { computed, ref } from 'vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Copy, Check, ExternalLink } from 'lucide-vue-next'
import { toast } from 'vue-sonner'
import { getBasePath } from '@/lib/api/client'

const copiedIndex = ref<number | null>(null)

/** 复制的是真实请求路径(带部署前缀),避免漏前缀或前缀重复。 */
const basePath = computed(() => getBasePath().replace(/\/$/, ''))

const apiEndpoints = computed(() => [
  {
    method: 'GET',
    path: `${basePath.value}/api/v1/runtime/release`,
    description: '读取当前生效版本',
  },
  {
    method: 'GET',
    path: `${basePath.value}/api/v1/runtime/release?version=<版本号>`,
    description: '固定读取指定已发布版本',
  },
  {
    method: 'GET',
    path: `${basePath.value}/api/v1/runtime/release/static?version=<版本号>&expected_snapshot_id=<UUID>`,
    description: '固定导出已展示快照的静态包(ZIP)',
  },
  {
    method: 'GET',
    path: `${basePath.value}/api/v1/runtime/release?content_format=raw`,
    description: '可选:content 以字符串原文返回,保留大整数精度',
    optional: true,
  },
])

const copyPath = async (path: string, index: number) => {
  try { await navigator.clipboard.writeText(path) }
  catch { toast.error('复制失败，请选择接口路径后手动复制'); return }
  copiedIndex.value = index
  toast.success('已复制到剪贴板')
  setTimeout(() => {
    copiedIndex.value = null
  }, 2000)
}
</script>

<template>
  <Card>
    <CardHeader>
      <div class="flex items-center justify-between">
        <div>
          <CardTitle class="text-lg">API 接入</CardTitle>
          <p class="text-sm text-muted-foreground mt-1">
            请求头需携带 x-environment 与 x-pipeline。
          </p>
        </div>
        <a
          href="https://yi-nology.github.io/rainbow_bridge/api/"
          target="_blank"
          rel="noopener noreferrer"
          class="text-sm text-muted-foreground hover:text-primary flex items-center gap-1"
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
        <div class="spectrum-api-path">
          <Badge class="spectrum-api-method font-mono text-xs shrink-0">
            {{ endpoint.method }}
          </Badge>
          <div class="min-w-0"><code>{{ endpoint.path }}</code><span class="spectrum-api-description">{{ endpoint.description }}</span></div>
          <Badge v-if="endpoint.optional" variant="secondary" class="text-xs shrink-0">可选</Badge>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <Button variant="ghost" size="icon" class="h-10 w-10" :aria-label="`复制接口路径 ${endpoint.path}`" @click="copyPath(endpoint.path, index)">
            <Check v-if="copiedIndex === index" class="w-4 h-4 text-green-600" />
            <Copy v-else class="w-4 h-4" />
          </Button>
        </div>
      </div>
    </CardContent>
  </Card>
</template>
