import { isSessionError } from '@/lib/api/http'
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { transferApi, type ExportSelection, type ExportTreeEnvironment, type ImportJsonData } from '@/lib/api/transfer'
import { toast } from 'vue-sonner'
import { captureScope } from '@/lib/scope'

export type { ExportTreeEnvironment }

export const useTransferStore = defineStore('transfer', () => {
  const exportTree = ref<ExportTreeEnvironment[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  const fetchExportTree = async () => {
    loading.value = true
    error.value = null
    try {
      const response = await transferApi.getExportTree()
      exportTree.value = response || []
    } catch (e) {
      error.value = e instanceof Error ? e.message : '获取导出树失败'
      if (!isSessionError(e)) toast.error(error.value)
    } finally {
      loading.value = false
    }
  }

  const migrate = async (
    sourceEnvKey: string,
    sourcePipelineKey: string,
    targetEnvKey: string,
    targetPipelineKey: string,
    resourceKeys: string[]
  ) => {
    loading.value = true
    try {
      const source = captureScope(sourceEnvKey, sourcePipelineKey)
      const target = captureScope(targetEnvKey, targetPipelineKey)
      await transferApi.migrate({
        sourceExpectedEnvironmentId: source.environmentId, sourceExpectedScopeId: source.scopeId,
        targetExpectedEnvironmentId: target.environmentId, targetExpectedScopeId: target.scopeId,
        sourceEnvironmentKey: sourceEnvKey,
        sourcePipelineKey: sourcePipelineKey,
        targetEnvironmentKey: targetEnvKey,
        targetPipelineKey: targetPipelineKey,
        resourceKeys: resourceKeys,
      })
      toast.success('配置迁移成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '配置迁移失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    } finally {
      loading.value = false
    }
  }

  const exportSelective = async (selections: ExportSelection[]) => {
    loading.value = true
    try {
      const response = await transferApi.exportSelective({
        source: 'editor',
        format: 'zip',
        selections,
      })
      toast.success('配置导出成功')
      return response
    } catch (e) {
      const message = e instanceof Error ? e.message : '配置导出失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    } finally {
      loading.value = false
    }
  }

  const importJson = async (data: unknown) => {
    loading.value = true
    try {
      await transferApi.importJson(data as ImportJsonData)
      toast.success('配置导入成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '配置导入失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    } finally {
      loading.value = false
    }
  }

  const importFile = async (file: File) => {
    loading.value = true
    try {
      await transferApi.importFile(file)
      toast.success('配置导入成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '配置导入失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    } finally {
      loading.value = false
    }
  }

  const importPreview = async (file: File) => {
    loading.value = true
    try {
      const response = await transferApi.importPreview(file)
      return response
    } catch (e) {
      const message = e instanceof Error ? e.message : '预览导入失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    } finally {
      loading.value = false
    }
  }

  const importSelective = async (file: File, selections: ExportSelection[], overwrite: boolean) => {
    loading.value = true
    try {
      await transferApi.importSelective(file, selections, overwrite)
      toast.success('选择性导入成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '选择性导入失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    } finally {
      loading.value = false
    }
  }

  return {
    reset: () => { exportTree.value = []; error.value = null; loading.value = false },
    exportTree,
    loading,
    error,
    fetchExportTree,
    migrate,
    exportSelective,
    importJson,
    importFile,
    importPreview,
    importSelective,
  }
})
