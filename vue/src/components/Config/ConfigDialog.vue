<script setup lang="ts">
import { ref, computed, nextTick, watch } from 'vue'
import { configApi } from '@/lib/api/config'
import { fromApiConfig } from '@/lib/api/transformers'
import { useDialogAttempt } from '@/composables/useDialogAttempt'
import { objectValueTokens, tokenDisplay } from '@/lib/config-json'
import { Dialog, DialogTrigger, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { Plus, X, Upload, AlertCircle, Loader2 } from 'lucide-vue-next'
import { useAssetStore } from '@/stores/asset'
import { type ConfigItem, type ConfigType, CONFIG_TYPE_META } from '@/lib/types'
import { resolveAssetUrl } from '@/lib/utils'

const props = defineProps<{
  open: boolean
  editingConfig: ConfigItem | null
  selectedEnvKey: string
  selectedPipelineKey: string
  selectedEnvironmentName: string | undefined
  selectedPipelineName: string | undefined
  /** 当前范围已冻结:写入口禁用,并说明原因 */
  frozen?: boolean
  frozenReason?: string
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'update:editingConfig', value: ConfigItem | null): void
  (e: 'create', config: {
    name: string
    alias: string
    type: ConfigType
    content: string
    description: string
  }, done: (ok: boolean, message?: string, kind?: string) => void): void
  (e: 'update', config: {
    id: string
    expectedEditRevision: string
    name: string
    alias: string
    type: ConfigType
    content: string
    description: string
  }, done: (ok: boolean, message?: string, kind?: string) => void): void
  (e: 'reset'): void
}>()

const assetStore = useAssetStore()

const formData = ref({
  name: '',
  alias: '',
  type: 'text' as ConfigType,
  content: '',
  description: '',
})

const keyValuePairs = ref<{ key: string; value: string }[]>([{ key: '', value: '' }])
let originalValueTokens = new Map<string, string>()
const booleanValue = ref(false)
const colorValue = ref('#3B82F6')
const errors = ref<{ field: string; message: string }[]>([])

const isEditing = computed(() => !!props.editingConfig)
const baseline = ref('')
const loadingDetail = ref(false)
const blocked = ref('')
const saving = ref(false)
const uploading = ref(false)
const busy = computed(() => saving.value || uploading.value || loadingDetail.value)
const formElement = ref<HTMLFormElement | null>(null)
const saveError = ref('')
const startAttempt = useDialogAttempt(
  () => [props.open, props.selectedEnvKey, props.selectedPipelineKey, props.editingConfig?.id],
  () => { saving.value = false; uploading.value = false },
)

function handleOpenChange(value: boolean) {
  if (!value && busy.value) return
  emit('update:open', value)
  if (!value) resetForm()
}

function focusFirstError() {
  void nextTick(() => {
    const field = errors.value[0]?.field
    const element = field ? formElement.value?.querySelector<HTMLElement>(`#${field}`) : null
    element?.focus()
    element?.scrollIntoView({ block: 'nearest' })
  })
}

function showSaveError(message: string) {
  saveError.value = message
  void nextTick(() => {
    const alert = formElement.value?.querySelector<HTMLElement>('.spectrum-config-save-error')
    alert?.focus()
    alert?.scrollIntoView({ block: 'nearest' })
  })
}

const getFieldError = (field: string) => {
  return errors.value.find(e => e.field === field)?.message
}

const resetForm = () => {
  originalValueTokens = new Map()
  formData.value = { name: '', alias: '', type: 'text', content: '', description: '' }
  keyValuePairs.value = [{ key: '', value: '' }]
  booleanValue.value = false
  colorValue.value = '#3B82F6'
  errors.value = []
  saveError.value = ''
  baseline.value = ''
  blocked.value = ''
  saving.value = false
  emit('update:editingConfig', null)
  emit('reset')
}

const handleTypeChange = (type: string) => {
  originalValueTokens = new Map()
  formData.value = { ...formData.value, type: type as ConfigType, content: '' }
  keyValuePairs.value = [{ key: '', value: '' }]
  booleanValue.value = false
  colorValue.value = '#3B82F6'
  errors.value = []
}

const getFinalContent = (): string => {
  switch (formData.value.type) {
    case 'keyvalue':
      const pairs = keyValuePairs.value.filter(p => p.key.trim())
      return '{' + pairs.map(p => {
        const original = originalValueTokens.get(p.key)
        const token = original !== undefined && tokenDisplay(original) === p.value ? original : JSON.stringify(p.value)
        return JSON.stringify(p.key) + ':' + token
      }).join(',') + '}'
    case 'boolean':
      return String(booleanValue.value)
    case 'color':
      return colorValue.value
    default:
      return formData.value.content
  }
}

const validateForm = (): boolean => {
  errors.value = []
  
  if (!formData.value.name.trim()) {
    errors.value.push({ field: 'name', message: '名称不能为空' })
  }

  if (!formData.value.alias.trim()) {
    errors.value.push({ field: 'alias', message: '别名不能为空' })
  } else if (!/^[A-Z_][A-Z0-9_]*$/i.test(formData.value.alias)) {
    errors.value.push({ field: 'alias', message: '别名只能包含字母、数字和下划线，且不能以数字开头' })
  }

  switch (formData.value.type) {
    case 'text':
    case 'textarea':
    case 'richtext':
      if (!formData.value.content.trim()) {
        errors.value.push({ field: 'content', message: '内容不能为空' })
      }
      break
    case 'number':
      if (!formData.value.content.trim()) {
        errors.value.push({ field: 'content', message: '请输入整数' })
      } else if (!/^-?\d+$/.test(formData.value.content)) {
        errors.value.push({ field: 'content', message: '请输入有效的整数' })
      }
      break
    case 'decimal':
      if (!formData.value.content.trim()) {
        errors.value.push({ field: 'content', message: '请输入小数' })
      } else if (!/^-?\d+(\.\d+)?$/.test(formData.value.content)) {
        errors.value.push({ field: 'content', message: '请输入有效的小数' })
      }
      break
    case 'object':
      if (!formData.value.content.trim()) {
        errors.value.push({ field: 'content', message: 'JSON对象不能为空' })
      } else {
        try {
          const parsed = JSON.parse(formData.value.content)
          if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
            errors.value.push({ field: 'content', message: '请输入有效的JSON对象（非数组）' })
          }
        } catch {
          errors.value.push({ field: 'content', message: 'JSON格式无效' })
        }
      }
      break
    case 'keyvalue':
      const validPairs = keyValuePairs.value.filter(p => p.key.trim() || p.value.trim())
      if (validPairs.length === 0) {
        errors.value.push({ field: 'content', message: '请至少添加一个键值对' })
      } else {
        const hasEmptyKey = validPairs.some(p => !p.key.trim())
        if (hasEmptyKey) {
          errors.value.push({ field: 'content', message: '键名不能为空' })
        }
        const keys = validPairs.map(p => p.key.trim()).filter(k => k)
        const uniqueKeys = new Set(keys)
        if (keys.length !== uniqueKeys.size) {
          errors.value.push({ field: 'content', message: '键名不能重复' })
        }
      }
      break
    case 'color':
      if (!/^#[0-9A-Fa-f]{6}$/.test(colorValue.value)) {
        errors.value.push({ field: 'content', message: '请输入有效的颜色值' })
      }
      break
    case 'file':
    case 'image':
      if (!formData.value.content.trim()) {
        errors.value.push({ field: 'content', message: formData.value.type === 'image' ? '请上传图片' : '请上传文件' })
      }
      break
  }

  if (errors.value.length) focusFirstError()
  return errors.value.length === 0
}

const handleAdd = () => {
  if (busy.value) return
  if (!validateForm()) return
  if (props.frozen) {
    showSaveError(`该范围已冻结,无法新增配置${props.frozenReason ? `(原因:${props.frozenReason})` : ''}`)
    return
  }
  saveError.value = ''
  const current = startAttempt()
  saving.value = true
  emit('create', {
    name: formData.value.name,
    alias: formData.value.alias,
    type: formData.value.type,
    content: getFinalContent(),
    description: formData.value.description,
  }, (ok, message) => {
    if (!current()) return
    saving.value = false
    if (ok) {
      emit('update:open', false)
      resetForm()
      return
    }
    // 失败时保留用户输入,不显示成功;服务端错误(含冻结范围)在此展示
    showSaveError(message || '保存失败')
  })
}

const handleUpdate = () => {
  if (blocked.value || !baseline.value || busy.value) return
  if (!validateForm() || !props.editingConfig) return
  if (props.frozen) {
    showSaveError(`该范围已冻结,无法修改配置${props.frozenReason ? `(原因:${props.frozenReason})` : ''}`)
    return
  }
  saveError.value = ''
  const current = startAttempt()
  saving.value = true
  emit('update', {
    id: props.editingConfig.id,
    expectedEditRevision: baseline.value,
    name: formData.value.name,
    alias: formData.value.alias,
    type: formData.value.type,
    content: getFinalContent(),
    description: formData.value.description,
  }, (ok, message, kind) => {
    if (!current()) return
    saving.value = false
    if (ok) {
      resetForm()
      return
    }
    blocked.value = ['CONFIG_EDIT_CONFLICT', 'CONFIG_EDIT_TARGET_NOT_FOUND', 'UNKNOWN_RESULT'].includes(kind || '') ? kind! : ''
    showSaveError(kind === 'CONFIG_EDIT_CONFLICT' ? '这条配置已被其他操作更新。你的修改尚未保存，请重新加载后核对。' : kind === 'UNKNOWN_RESULT' ? '保存结果未确认，请重新加载核对' : message || '保存失败')
  })
}

const handleFileUpload = async (type: 'file' | 'image') => {
  if (busy.value) return
  if (props.frozen) {
    showSaveError(`该范围已冻结,无法上传资源${props.frozenReason ? `(原因:${props.frozenReason})` : ''}`)
    return
  }
  const input = document.createElement('input')
  input.type = 'file'
  input.accept = type === 'image' ? 'image/*' : '*/*'
  const current = startAttempt()
  const environmentKey = props.selectedEnvKey
  const pipelineKey = props.selectedPipelineKey

  input.onchange = async (e) => {
      const file = (e.target as HTMLInputElement).files?.[0]
      if (!current() || !file || !environmentKey || !pipelineKey) return

      uploading.value = true
      saveError.value = ''
      try {
        const result = await assetStore.uploadAsset(file, environmentKey, pipelineKey)
        if (!current()) return
        if (result?.url) {
          // 只保存相对路径，避免重复添加 basePath
          const basePath = import.meta.env.BASE_URL || ''
          const normalizedBasePath = basePath.replace(/\/$/, '')
          let url = result.url
          if (normalizedBasePath && url.startsWith(normalizedBasePath)) {
            url = url.replace(normalizedBasePath, '')
          }
          formData.value.content = url
          errors.value = errors.value.filter(e => e.field !== 'content')
        }
      } catch (e) {
        if (!current()) return
        showSaveError(e instanceof Error ? e.message : '资源上传失败')
      } finally {
        if (current()) uploading.value = false
      }
    }

  input.click()
}

const addKeyValuePair = () => {
  keyValuePairs.value.push({ key: '', value: '' })
}

const removeKeyValuePair = (index: number) => {
  keyValuePairs.value = keyValuePairs.value.filter((_, i) => i !== index)
}

const updateKeyValuePair = (index: number, field: 'key' | 'value', value: string) => {
  keyValuePairs.value[index][field] = value
}

// 初始化编辑数据
function initializeEdit(config: ConfigItem) {

  formData.value = {
    name: config.name,
    alias: config.alias,
    type: config.type,
    content: config.content,
    description: config.description || '',
  }

  originalValueTokens = new Map()
  keyValuePairs.value = [{ key: '', value: '' }]
  if (config.type === 'keyvalue') {
    try {
      originalValueTokens = objectValueTokens(config.content)
      keyValuePairs.value = Array.from(originalValueTokens, ([key, value]) => ({ key, value: tokenDisplay(value) }))
    } catch {
      keyValuePairs.value = [{ key: '', value: '' }]
    }
  } else if (config.type === 'boolean') {
    booleanValue.value = config.content === 'true'
  } else if (config.type === 'color') {
    colorValue.value = config.content || '#3B82F6'
  }
}

async function loadEdit(confirmReplace = false) {
  if (confirmReplace && !window.confirm('将用最新配置替换当前未保存内容')) return
  const target = props.editingConfig
  if (!target) return
  const current = startAttempt()
  loadingDetail.value = true
  try {
    const response = await configApi.detail(target.id, { environmentKey: props.selectedEnvKey, pipelineKey: props.selectedPipelineKey })
    if (!current()) return
    if (!response.config?.editRevision) throw new Error('配置编辑版本无效，请重新加载')
    const config = fromApiConfig(response.config)
    initializeEdit(config)
    baseline.value = config.editRevision!
    blocked.value = ''
    saveError.value = ''
    initialSignature.value = formSignature()
  } catch (error) {
    if (current()) showSaveError(error instanceof Error ? error.message : '加载失败，草稿已保留')
  } finally { if (current()) loadingDetail.value = false }
}
async function copyDraft() {
  try { await navigator.clipboard.writeText(JSON.stringify({ ...formData.value, content: getFinalContent() }, null, 2)) }
  catch { showSaveError('复制失败，请手动复制草稿') }
}
watch(() => [props.open, props.selectedEnvKey, props.selectedPipelineKey, props.editingConfig?.id], () => {
  baseline.value = ''
  blocked.value = ''
  loadingDetail.value = false
  if (props.open && props.editingConfig) void loadEdit()
}, { immediate: true, flush: 'post' })

const formSignature = () => JSON.stringify({ ...formData.value, content: getFinalContent() })
const initialSignature = ref(formSignature())
const isDirty = computed(() => formSignature() !== initialSignature.value)
</script>

<template>
  <Dialog :open="open" @update:open="handleOpenChange">
    <DialogTrigger as-child>
      <Button v-if="!isEditing" class="h-10 px-4" :disabled="frozen || !selectedEnvKey || !selectedPipelineKey" :title="frozen ? `已冻结:${frozenReason || '该范围不可编辑'}` : undefined">
        <Plus class="w-4 h-4 mr-2" />
        新增配置
      </Button>
    </DialogTrigger>
    <!-- §2.1 / §3.6 / §6.2: fixed context and action bar; one scrollable field area.
         Keep the 640px form width, 24/32px grouping and current Spectrum tokens. -->
    <DialogContent
      class="spectrum-config-dialog"
      :close-disabled="busy"
      @pointer-down-outside="(event) => { if (isDirty || busy) event.preventDefault() }"
      @escape-key-down="(event) => { if (busy) event.preventDefault() }"
    >
      <DialogHeader class="spectrum-config-dialog-header">
        <DialogTitle class="text-xl font-semibold">{{ isEditing ? '编辑配置' : '新增配置' }}</DialogTitle>
        <DialogDescription class="text-muted-foreground">
          {{ selectedEnvironmentName }} / {{ selectedPipelineName }}
        </DialogDescription>
      </DialogHeader>
      <form ref="formElement" class="spectrum-config-dialog-form" novalidate :aria-busy="busy" @submit.prevent="isEditing ? handleUpdate() : handleAdd()">
      <div class="spectrum-config-dialog-body">
      <fieldset class="spectrum-config-fields" :disabled="busy || isEditing && !baseline">
        <div v-if="saveError" role="alert" tabindex="-1" class="spectrum-config-save-error p-3 rounded-md bg-destructive/10 border border-destructive/20">
          <div class="flex items-center gap-2 text-destructive font-medium text-sm">
            <AlertCircle class="w-4 h-4" />
            {{ saveError }}
          </div>
        </div>

        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <div class="space-y-3">
            <Label for="name" class="text-sm font-medium">名称 *</Label>
            <Input
              id="name"
              placeholder="例如: API基础地址"
              v-model="formData.name"
              aria-required="true"
              :aria-invalid="Boolean(getFieldError('name'))"
              :aria-describedby="getFieldError('name') ? 'name-error' : undefined"
              :class="getFieldError('name') ? 'border-destructive' : ''"
              class="h-10"
            />
            <p v-if="getFieldError('name')" id="name-error" role="alert" class="text-sm text-destructive flex items-center gap-1">
              <AlertCircle class="w-3 h-3" />
              {{ getFieldError('name') }}
            </p>
          </div>
          <div class="space-y-3">
            <Label for="alias" class="text-sm font-medium">别名 *</Label>
            <Input
              id="alias"
              placeholder="例如: API_BASE_URL"
              v-model="formData.alias"
              aria-required="true"
              :aria-invalid="Boolean(getFieldError('alias'))"
              :aria-describedby="getFieldError('alias') ? 'alias-error' : undefined"
              :class="getFieldError('alias') ? 'border-destructive' : ''"
              :disabled="isEditing"
              class="h-10"
            />
            <p v-if="getFieldError('alias')" id="alias-error" role="alert" class="text-sm text-destructive flex items-center gap-1">
              <AlertCircle class="w-3 h-3" />
              {{ getFieldError('alias') }}
            </p>
            <p v-if="isEditing" class="text-xs text-muted-foreground">别名在创建后不可修改</p>
          </div>
        </div>

        <div class="space-y-3">
          <Label for="type" class="text-sm font-medium">类型</Label>
          <Select :model-value="formData.type" @update:model-value="handleTypeChange" :disabled="isEditing || busy">
            <SelectTrigger id="type" aria-label="配置类型" class="h-10">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="[key, config] in Object.entries(CONFIG_TYPE_META)" :key="key" :value="key">
                <div class="flex items-center gap-2">
                  <Badge variant="secondary" class="text-xs px-2 py-0.5">
                    {{ config.label }}
                  </Badge>
                  <span class="text-muted-foreground text-xs">{{ config.description }}</span>
                </div>
              </SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div class="space-y-3">
          <Label for="content" class="text-sm font-medium">内容</Label>
          <Input
            v-if="formData.type === 'text' || formData.type === 'number' || formData.type === 'decimal'"
            id="content"
            :placeholder="formData.type === 'text' ? '请输入文本内容...' : '请输入数值...'"
            v-model="formData.content"
            :aria-invalid="Boolean(getFieldError('content'))"
            :aria-describedby="getFieldError('content') ? 'content-error' : undefined"
            :class="getFieldError('content') ? 'border-destructive' : ''"
            class="h-10"
          />
          <Textarea
            v-else-if="formData.type === 'textarea' || formData.type === 'richtext' || formData.type === 'object'"
            id="content"
            :placeholder="formData.type === 'object' ? '{&quot;key&quot;: &quot;value&quot;}' : '请输入内容...'"
            :rows="formData.type === 'object' ? 8 : 4"
            v-model="formData.content"
            :aria-invalid="Boolean(getFieldError('content'))"
            :aria-describedby="getFieldError('content') ? 'content-error' : undefined"
            :class="getFieldError('content') ? 'border-destructive' : ''"
            class="resize-none"
          />
          <div v-else-if="formData.type === 'boolean'" class="flex items-center gap-4 p-4 border rounded-md bg-muted/50 dark:bg-muted">
            <Switch v-model="booleanValue" :disabled="busy" aria-label="配置布尔值" />
            <span class="text-sm">
              当前值: <Badge :variant="booleanValue ? 'default' : 'secondary'" class="ml-1">{{ booleanValue ? 'true' : 'false' }}</Badge>
            </span>
          </div>
          <div v-else-if="formData.type === 'color'" class="flex items-center gap-4">
            <input
              type="color"
              aria-label="选择配置颜色"
              :value="colorValue"
              @input="colorValue = ($event.target as HTMLInputElement).value"
              class="w-20 h-20 rounded-lg cursor-pointer border-2 border-border shadow-sm"
            />
            <Input
              id="content"
              aria-label="颜色值"
              :model-value="colorValue"
              @update:model-value="colorValue = $event"
              class="font-mono text-lg h-10 flex-1"
              maxlength="7"
            />
          </div>
          <div v-else-if="formData.type === 'keyvalue'" id="content" tabindex="-1" class="space-y-3">
            <div class="flex items-center justify-between">
              <Label class="text-sm font-medium">键值对</Label>
              <Button type="button" variant="outline" size="sm" @click="addKeyValuePair" class="h-8">
                <Plus class="w-3 h-3 mr-1" />
                添加
              </Button>
            </div>
            <div class="space-y-2 max-h-64 overflow-y-auto p-2 border rounded-md bg-muted/50 dark:bg-muted">
              <div v-for="(pair, index) in keyValuePairs" :key="index" class="flex items-center gap-2">
                <Input
                  placeholder="键名"
                  :aria-label="`键名 ${index + 1}`"
                  :model-value="pair.key"
                  @update:model-value="updateKeyValuePair(index, 'key', $event)"
                  class="flex-1 h-9"
                />
                <span class="text-muted-foreground font-medium">=</span>
                <Input
                  placeholder="键值"
                  :aria-label="`键值 ${index + 1}`"
                  :model-value="pair.value"
                  @update:model-value="updateKeyValuePair(index, 'value', $event)"
                  class="flex-1 h-9"
                />
                <Button
                  v-if="keyValuePairs.length > 1"
                  type="button"
                  variant="ghost"
                  size="icon"
                  :aria-label="`移除键值对 ${index + 1}`"
                  @click="removeKeyValuePair(index)"
                  class="h-9 w-9"
                >
                  <X class="w-4 h-4" />
                </Button>
              </div>
            </div>
          </div>
          <button
            v-else-if="formData.type === 'file' || formData.type === 'image'"
            id="content"
            type="button"
            :disabled="busy || frozen"
            :aria-label="`上传${formData.type === 'image' ? '图片' : '文件'}`"
            class="spectrum-config-upload border border-dashed rounded-lg p-6 text-center transition-colors"
            :class="[getFieldError('content') ? 'border-destructive' : '', frozen ? 'opacity-60 pointer-events-none' : '']"
            :title="frozen ? `已冻结:${frozenReason || '该范围不可上传资源'}` : undefined"
            @click="handleFileUpload(formData.type)"
          >
            <Loader2 v-if="uploading" class="w-6 h-6 mx-auto animate-spin" />
            <span v-if="uploading" role="status" class="block mt-3">上传中…</span>
            <Upload v-else-if="!formData.content" class="w-8 h-8 mx-auto text-muted-foreground" />
            <p v-if="!uploading && !formData.content" class="text-sm text-muted-foreground mt-3">
              点击上传{{ formData.type === 'image' ? '图片' : '文件' }}
            </p>
            <img
              v-else-if="!uploading && formData.type === 'image' && formData.content"
              :src="resolveAssetUrl(formData.content)"
              alt="预览"
              class="max-w-full max-h-64 mx-auto object-contain rounded-md shadow-sm"
            />
            <p v-else-if="!uploading" class="text-sm truncate">{{ formData.content }}</p>
          </button>
          <p v-if="getFieldError('content')" id="content-error" role="alert" class="text-sm text-destructive flex items-center gap-1">
            <AlertCircle class="w-3 h-3" />
            {{ getFieldError('content') }}
          </p>
        </div>

        <div class="space-y-3">
          <Label for="description" class="text-sm font-medium">描述</Label>
          <Textarea
            id="description"
            placeholder="选填"
            :rows="2"
            v-model="formData.description"
            class="resize-none"
          />
        </div>
      </fieldset>
      </div>
        <DialogFooter class="spectrum-config-dialog-footer">
          <Button v-if="blocked" type="button" variant="outline" :disabled="busy" @click="copyDraft">复制我的草稿</Button>
          <Button v-if="(isEditing && !baseline) || (blocked && blocked !== 'CONFIG_EDIT_TARGET_NOT_FOUND')" type="button" variant="outline" :disabled="busy" @click="loadEdit(true)">重新加载</Button>
          <Button variant="outline" :disabled="busy" @click="handleOpenChange(false)" class="h-10">
            取消
          </Button>
          <Button type="submit" class="h-10" :disabled="busy || frozen || Boolean(blocked) || isEditing && !baseline">
            <Loader2 v-if="saving" class="w-4 h-4 mr-2 animate-spin" />
            {{ saving ? '保存中...' : '保存' }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
