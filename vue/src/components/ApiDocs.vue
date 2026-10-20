<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
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
    description: '读取当前生效版本(请求头 x-environment / x-pipeline)',
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
  {
    method: 'GET',
    path: `${basePath.value}/api/v1/runtime/config`,
    description: '兼容旧接口:读取编辑区,不受发布切换控制',
    legacy: true,
  },
  {
    method: 'GET',
    path: `${basePath.value}/api/v1/runtime/static`,
    description: '兼容旧接口:导出编辑区静态包',
    legacy: true,
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
          <CardTitle class="text-lg">前端对接</CardTitle>
          <p class="text-sm text-muted-foreground mt-1">
            业务应用读取生效配置需要携带环境与渠道请求头;旧接口读取编辑区,不受发布切换控制
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
          <Badge v-if="endpoint.legacy" variant="outline" class="text-xs shrink-0">兼容</Badge>
          <Badge v-else-if="endpoint.optional" variant="secondary" class="text-xs shrink-0">可选</Badge>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <Button variant="ghost" size="icon" class="h-10 w-10" :aria-label="`复制接口路径 ${endpoint.path}`" @click="copyPath(endpoint.path, index)">
            <Check v-if="copiedIndex === index" class="w-4 h-4 text-green-600" />
            <Copy v-else class="w-4 h-4" />
          </Button>
        </div>
      </div>
      <p class="text-xs text-muted-foreground pt-1">
        需要发布新版本或切换生效版本请前往
        <RouterLink to="/releases" class="text-primary hover:underline">版本发布</RouterLink>
        ;编辑区内容在
        <RouterLink to="/config" class="text-primary hover:underline">配置管理</RouterLink>
        修改后需先发布才会影响上面的生效读取。
      </p>
    </CardContent>
  </Card>
</template>
