<script setup lang="ts">
import { ref, computed } from 'vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Dialog, DialogTrigger, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { Search, Pencil, Trash2, Loader2, Layers } from 'lucide-vue-next'
import { type ConfigItem } from '@/lib/types'
import { CONFIG_TYPE_META } from '@/lib/types'
import ConfigContent from './ConfigContent.vue'
import ConfigDialog from './ConfigDialog.vue'

const props = defineProps<{
  configs: ConfigItem[]
  loading: boolean
  selectedEnvKey: string
  selectedPipelineKey: string
  selectedEnvironmentName: string | undefined
  selectedPipelineName: string | undefined
  /** 当前范围已冻结:禁止配置新增/编辑/删除,并说明原因 */
  frozen?: boolean
  frozenReason?: string
}>()

const emit = defineEmits<{
  (e: 'search', value: string): void
  (e: 'create', config: {
    name: string
    alias: string
    type: string
    content: string
    description: string
  }, done: (ok: boolean, message?: string) => void): void
  (e: 'update', config: {
    id: string
    name: string
    alias: string
    type: string
    content: string
    description: string
  }, done: (ok: boolean, message?: string) => void): void
  (e: 'delete', id: string): void
  (e: 'imagePreview', url: string, name: string): void
}>()

const searchTerm = ref('')
const isAddDialogOpen = ref(false)
const editingConfig = ref<ConfigItem | null>(null)

const filteredConfigs = computed(() => {
  if (!searchTerm.value) return props.configs
  return props.configs.filter(
    (config) =>
      config.name.toLowerCase().includes(searchTerm.value.toLowerCase()) ||
      config.alias.toLowerCase().includes(searchTerm.value.toLowerCase())
  )
})

const handleSearch = (value: string) => {
  searchTerm.value = value
  emit('search', value)
}

const handleEdit = (config: ConfigItem) => {
  editingConfig.value = config
}

const handleDelete = (id: string) => {
  emit('delete', id)
}

const handleImagePreview = (url: string, name: string = '图片预览') => {
  emit('imagePreview', url, name)
}

const handleCreate = (config: {
  name: string
  alias: string
  type: string
  content: string
  description: string
}, done: (ok: boolean, message?: string) => void) => {
  emit('create', config, done)
}

const handleUpdate = (config: {
  id: string
  name: string
  alias: string
  type: string
  content: string
  description: string
}, done: (ok: boolean, message?: string) => void) => {
  emit('update', config, done)
}

const handleReset = () => {
  editingConfig.value = null
}
</script>

<template>
  <Card class="overflow-hidden shadow-md hover:shadow-lg transition-shadow duration-300">
    <CardHeader class="bg-gray-50 dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700">
      <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
        <CardTitle class="text-xl font-semibold text-gray-900 dark:text-white flex items-center gap-2">
          配置列表
          <Badge v-if="frozen" variant="destructive" class="px-2 py-0.5 text-xs" :title="frozenReason || undefined">
            已冻结
          </Badge>
        </CardTitle>
        <div class="flex items-center gap-4">
          <div class="relative">
            <Search class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
            <Input
              placeholder="搜索配置..."
              class="pl-9 w-72 h-10"
              v-model="searchTerm"
              @input="handleSearch($event.target.value)"
              :disabled="!selectedEnvKey || !selectedPipelineKey"
            />
          </div>
          <ConfigDialog
            :open="isAddDialogOpen"
            :editing-config="editingConfig"
            :selected-env-key="selectedEnvKey"
            :selected-pipeline-key="selectedPipelineKey"
            :selected-environment-name="selectedEnvironmentName"
            :selected-pipeline-name="selectedPipelineName"
            :frozen="frozen"
            :frozen-reason="frozenReason"
            @update:open="isAddDialogOpen = $event"
            @update:editing-config="editingConfig = $event"
            @create="handleCreate"
            @update="handleUpdate"
            @reset="handleReset"
          />
        </div>
      </div>
    </CardHeader>
    <CardContent class="p-6">
      <div v-if="!selectedEnvKey || !selectedPipelineKey" class="flex flex-col items-center justify-center py-20 text-center">
        <div class="w-20 h-20 rounded-full bg-muted flex items-center justify-center mb-6">
          <Layers class="w-10 h-10 text-muted-foreground" />
        </div>
        <h3 class="text-xl font-semibold mb-3 text-gray-900 dark:text-white">请先选择环境和渠道</h3>
        <p class="text-muted-foreground text-sm max-w-md">
          配置项与环境和渠道关联，请在上方选择要管理的环境和渠道后查看对应的配置列表
        </p>
      </div>
      <div v-else-if="loading" class="flex items-center justify-center py-20">
        <Loader2 class="w-10 h-10 animate-spin text-muted-foreground" />
      </div>
      <div v-else class="border rounded-lg overflow-hidden shadow-sm">
        <Table class="w-full">
          <TableHeader class="bg-gray-50 dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700">
            <TableRow>
              <TableHead class="w-64 py-4 px-6 text-left text-sm font-semibold text-gray-900 dark:text-white">名称</TableHead>
              <TableHead class="w-48 py-4 px-6 text-left text-sm font-semibold text-gray-900 dark:text-white">别名</TableHead>
              <TableHead class="w-32 py-4 px-6 text-left text-sm font-semibold text-gray-900 dark:text-white">类型</TableHead>
              <TableHead class="py-4 px-6 text-left text-sm font-semibold text-gray-900 dark:text-white">内容</TableHead>
              <TableHead class="w-40 py-4 px-6 text-right text-sm font-semibold text-gray-900 dark:text-white">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-if="filteredConfigs.length === 0" class="hover:bg-gray-50 dark:hover:bg-gray-800">
              <TableCell colspan="5" class="text-center py-12 text-muted-foreground">
                {{ searchTerm ? '未找到匹配的配置' : '当前环境和渠道下暂无配置' }}
              </TableCell>
            </TableRow>
            <TableRow v-for="config in filteredConfigs" :key="config.id" class="hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors">
              <TableCell class="py-4 px-6 font-medium text-gray-900 dark:text-white break-all max-w-xs">
                {{ config.name }}
                <p v-if="config.description" class="text-xs text-muted-foreground mt-1 font-normal line-clamp-2">{{ config.description }}</p>
              </TableCell>
              <TableCell class="py-4 px-6 text-muted-foreground break-all max-w-xs font-mono text-sm">
                {{ config.alias }}
              </TableCell>
              <TableCell class="py-4 px-6">
                <Badge :class="CONFIG_TYPE_META[config.type]?.color || ''" class="px-2 py-1 border">
                  {{ CONFIG_TYPE_META[config.type]?.label || config.type }}
                </Badge>
              </TableCell>
              <TableCell class="py-4 px-6">
                <ConfigContent :config="config" :on-image-preview="handleImagePreview" />
              </TableCell>
              <TableCell class="py-4 px-6 text-right">
                <div class="flex items-center justify-end gap-2">
                  <Dialog :open="editingConfig?.id === config.id" @update:open="(open) => { if (!open) editingConfig = null; else handleEdit(config) }">
                    <DialogTrigger as-child>
                      <Button variant="ghost" size="icon" @click="handleEdit(config)" :disabled="frozen" :title="frozen ? `已冻结:${frozenReason || '该范围不可编辑配置'}` : undefined" class="h-9 w-9 hover:bg-gray-100 dark:hover:bg-gray-700">
                        <Pencil class="w-4 h-4 text-gray-600 dark:text-gray-300" />
                      </Button>
                    </DialogTrigger>
                    <DialogContent class="max-w-xl max-h-[85vh] overflow-y-auto rounded-lg border-0 shadow-2xl">
                      <DialogHeader>
                        <DialogTitle class="text-xl font-semibold">编辑配置</DialogTitle>
                        <DialogDescription class="text-muted-foreground">修改配置项信息</DialogDescription>
                      </DialogHeader>
                      <ConfigDialog
                        :open="editingConfig?.id === config.id"
                        :editing-config="editingConfig"
                        :selected-env-key="selectedEnvKey"
                        :selected-pipeline-key="selectedPipelineKey"
                        :selected-environment-name="selectedEnvironmentName"
                        :selected-pipeline-name="selectedPipelineName"
                        :frozen="frozen"
                        :frozen-reason="frozenReason"
                        @update:open="(value) => { if (!value) editingConfig = null }"
                        @update:editing-config="editingConfig = $event"
                        @update="handleUpdate"
                        @reset="handleReset"
                      />
                    </DialogContent>
                  </Dialog>
                  <Button
                    variant="ghost"
                    size="icon"
                    :disabled="loading || frozen"
                    :title="frozen ? `已冻结:${frozenReason || '该范围不可删除配置'}` : undefined"
                    @click="handleDelete(config.id)"
                    class="h-9 w-9 hover:bg-gray-100 dark:hover:bg-gray-700"
                  >
                    <Loader2 v-if="loading" class="w-4 h-4 animate-spin" />
                    <Trash2 v-else class="w-4 h-4 text-destructive" />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </CardContent>
  </Card>
</template>