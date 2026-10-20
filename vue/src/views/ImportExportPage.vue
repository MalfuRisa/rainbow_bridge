<script setup lang="ts">
import { ref, onMounted } from 'vue'
import SpectrumLayout from '@/components/spectrum/SpectrumLayout.vue'
import SpectrumPageHeader from '@/components/spectrum/SpectrumPageHeader.vue'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { FolderDown, FolderUp } from 'lucide-vue-next'
import ExportFunctionality from '@/components/ImportExport/ExportFunctionality.vue'
import ImportFunctionality from '@/components/ImportExport/ImportFunctionality.vue'
import { transferApi, type ExportTreeEnvironment } from '@/lib/api/transfer'

const exportTree = ref<ExportTreeEnvironment[]>([])
const isLoadingTree = ref(true)
const treeError = ref('')

onMounted(() => {
  loadExportTree()
})

const loadExportTree = async () => {
  isLoadingTree.value = true
  treeError.value = ''
  try {
    const tree = await transferApi.getExportTree()
    exportTree.value = tree
  } catch (err) {
    treeError.value = err instanceof Error ? err.message : '加载导出内容失败'
  } finally {
    isLoadingTree.value = false
  }
}
</script>

<template>
  <SpectrumLayout>
    <div class="spectrum-operations spectrum-transfer-page">
    <SpectrumPageHeader eyebrow="ARCHIVE / IMPORT & EXPORT" title="导入导出" description="把配置打包带走，或从备份中有选择地恢复。" />
    <div v-if="treeError" class="spectrum-error" role="alert">{{ treeError }}<Button variant="outline" @click="loadExportTree">重新加载导出内容</Button></div>
    <Tabs default-value="export" class="space-y-6">
      <TabsList class="spectrum-transfer-tabs">
        <TabsTrigger value="export" class="gap-2">
          <FolderDown class="w-4 h-4" />
          数据导出
        </TabsTrigger>
        <TabsTrigger value="import" class="gap-2">
          <FolderUp class="w-4 h-4" />
          数据导入
        </TabsTrigger>
      </TabsList>

      <TabsContent value="export" class="space-y-6">
        <ExportFunctionality 
          :export-tree="exportTree" 
          :is-loading-tree="isLoadingTree"
          @load-tree="loadExportTree"
        />
      </TabsContent>

      <TabsContent value="import" class="space-y-6">
        <ImportFunctionality @load-tree="loadExportTree" />
      </TabsContent>
    </Tabs>
    </div>
  </SpectrumLayout>
</template>
