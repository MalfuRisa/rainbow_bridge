<script setup lang="ts">
import { ref, computed } from 'vue'
import { objectValueTokens, tokenDisplay } from '@/lib/config-json'
import { Dialog, DialogTrigger, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { Plus, X, Upload, AlertCircle } from 'lucide-vue-next'
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
  }, done: (ok: boolean, message?: string) => void): void
  (e: 'update', config: {
    id: string
    name: string
    alias: string
    type: ConfigType
    content: string
    description: string
  }, done: (ok: boolean, message?: string) => void): void
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
const saving = ref(false)
const saveError = ref('')

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

  return errors.value.length === 0
}

const handleAdd = () => {
  if (!validateForm()) return
  if (props.frozen) {
    saveError.value = `该范围已冻结,无法新增配置${props.frozenReason ? `(原因:${props.frozenReason})` : ''}`
    return
  }
  saveError.value = ''
  saving.value = true
  emit('create', {
    name: formData.value.name,
    alias: formData.value.alias,
    type: formData.value.type,
    content: getFinalContent(),
    description: formData.value.description,
  }, (ok, message) => {
    saving.value = false
    if (ok) {
      emit('update:open', false)
      resetForm()
      return
    }
    // 失败时保留用户输入,不显示成功;服务端错误(含冻结范围)在此展示
    saveError.value = message || '保存失败'
  })
}

const handleUpdate = () => {
  if (!validateForm() || !props.editingConfig) return
  if (props.frozen) {
    saveError.value = `该范围已冻结,无法修改配置${props.frozenReason ? `(原因:${props.frozenReason})` : ''}`
    return
  }
  saveError.value = ''
  saving.value = true
  emit('update', {
    id: props.editingConfig.id,
    name: formData.value.name,
    alias: formData.value.alias,
    type: formData.value.type,
    content: getFinalContent(),
    description: formData.value.description,
  }, (ok, message) => {
    saving.value = false
    if (ok) {
      resetForm()
      return
    }
    saveError.value = message || '保存失败'
  })
}

const handleFileUpload = async (type: 'file' | 'image') => {
  if (props.frozen) {
    saveError.value = `该范围已冻结,无法上传资源${props.frozenReason ? `(原因:${props.frozenReason})` : ''}`
    return
  }
  const input = document.createElement('input')
  input.type = 'file'
  input.accept = type === 'image' ? 'image/*' : '*/*'

  input.onchange = async (e) => {
      const file = (e.target as HTMLInputElement).files?.[0]
      if (!file || !props.selectedEnvKey || !props.selectedPipelineKey) return

      try {
        const result = await assetStore.uploadAsset(file, props.selectedEnvKey, props.selectedPipelineKey)
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
        console.error('上传失败:', e)
        saveError.value = e instanceof Error ? e.message : '资源上传失败'
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
if (isEditing.value && props.editingConfig) {
  let formattedContent = props.editingConfig.content
  if (props.editingConfig.type === 'object' && props.editingConfig.content.trim()) {
    try {
      JSON.parse(props.editingConfig.content)
      formattedContent = props.editingConfig.content
    } catch {
      formattedContent = props.editingConfig.content
    }
  }

  formData.value = {
    name: props.editingConfig.name,
    alias: props.editingConfig.alias,
    type: props.editingConfig.type,
    content: formattedContent,
    description: props.editingConfig.description || '',
  }

  if (props.editingConfig.type === 'keyvalue') {
    try {
      originalValueTokens = objectValueTokens(props.editingConfig.content)
      keyValuePairs.value = Array.from(originalValueTokens, ([key, value]) => ({ key, value: tokenDisplay(value) }))
    } catch {
      keyValuePairs.value = [{ key: '', value: '' }]
    }
  } else if (props.editingConfig.type === 'boolean') {
    booleanValue.value = props.editingConfig.content === 'true'
  } else if (props.editingConfig.type === 'color') {
    colorValue.value = props.editingConfig.content || '#3B82F6'
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(value) => emit('update:open', value)">
    <DialogTrigger as-child>
      <Button v-if="!isEditing" class="h-10 px-4" :disabled="frozen || !selectedEnvKey || !selectedPipelineKey" :title="frozen ? `已冻结:${frozenReason || '该范围不可编辑'}` : undefined">
        <Plus class="w-4 h-4 mr-2" />
        新增配置
      </Button>
    </DialogTrigger>
    <DialogContent class="spectrum-config-dialog max-w-xl max-h-[85vh] overflow-y-auto rounded-lg border-0 shadow-2xl">
      <DialogHeader>
        <DialogTitle class="text-xl font-semibold">{{ isEditing ? '编辑配置' : '新增配置' }}</DialogTitle>
        <DialogDescription class="text-muted-foreground">
          {{ selectedEnvironmentName }} / {{ selectedPipelineName }}
        </DialogDescription>
      </DialogHeader>
      <div class="space-y-5 py-4">
        <div v-if="saveError" class="p-4 rounded-md bg-destructive/10 border border-destructive/20">
          <div class="flex items-center gap-2 text-destructive font-medium text-sm">
            <AlertCircle class="w-4 h-4" />
            {{ saveError }}
          </div>
        </div>
        <div v-if="errors.length > 0" class="p-4 rounded-md bg-destructive/10 border border-destructive/20">
          <div class="flex items-center gap-2 text-destructive font-medium text-sm mb-2">
            <AlertCircle class="w-4 h-4" />
            保存失败，请检查以下问题：
          </div>
          <ul class="list-disc list-inside text-sm text-destructive/80 space-y-1">
            <li v-for="(error, index) in errors" :key="index">{{ error.message }}</li>
          </ul>
        </div>

        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <div class="space-y-3">
            <Label for="name" class="text-sm font-medium">名称 *</Label>
            <Input
              id="name"
              placeholder="例如: API基础地址"
              v-model="formData.name"
              :class="getFieldError('name') ? 'border-destructive' : ''"
              class="h-10"
            />
            <p v-if="getFieldError('name')" class="text-sm text-destructive flex items-center gap-1">
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
              :class="getFieldError('alias') ? 'border-destructive' : ''"
              :disabled="isEditing"
              class="h-10"
            />
            <p v-if="getFieldError('alias')" class="text-sm text-destructive flex items-center gap-1">
              <AlertCircle class="w-3 h-3" />
              {{ getFieldError('alias') }}
            </p>
            <p v-if="isEditing" class="text-xs text-muted-foreground">别名在创建后不可修改</p>
          </div>
        </div>

        <div class="space-y-3">
          <Label for="type" class="text-sm font-medium">类型</Label>
          <Select :model-value="formData.type" @update:model-value="handleTypeChange" :disabled="isEditing">
            <SelectTrigger class="h-10">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="[key, config] in Object.entries(CONFIG_TYPE_META)" :key="key" :value="key">
                <div class="flex items-center gap-2">
                  <Badge variant="secondary" :class="config.color + ' text-xs px-2 py-0.5'">
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
            :class="getFieldError('content') ? 'border-destructive' : ''"
            class="h-10"
          />
          <Textarea
            v-else-if="formData.type === 'textarea' || formData.type === 'richtext' || formData.type === 'object'"
            id="content"
            :placeholder="formData.type === 'object' ? '{&quot;key&quot;: &quot;value&quot;}' : '请输入内容...'"
            :rows="formData.type === 'object' ? 8 : 4"
            v-model="formData.content"
            :class="getFieldError('content') ? 'border-destructive' : ''"
            class="resize-none"
          />
          <div v-else-if="formData.type === 'boolean'" class="flex items-center gap-4 p-4 border rounded-md bg-muted/50 dark:bg-muted">
            <Switch v-model="booleanValue" aria-label="配置布尔值" />
            <span class="text-sm">
              当前值: <Badge :variant="booleanValue ? 'default' : 'secondary'" class="ml-1">{{ booleanValue ? 'true' : 'false' }}</Badge>
            </span>
          </div>
          <div v-else-if="formData.type === 'color'" class="flex items-center gap-4">
            <input
              type="color"
              :value="colorValue"
              @input="colorValue = ($event.target as HTMLInputElement).value"
              class="w-20 h-20 rounded-lg cursor-pointer border-2 border-border shadow-sm"
            />
            <Input
              :value="colorValue"
              @update:model-value="colorValue = $event"
              class="font-mono text-lg h-10 flex-1"
              maxlength="7"
            />
          </div>
          <div v-else-if="formData.type === 'keyvalue'" class="space-y-3">
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
                  :value="pair.key"
                  @update:model-value="updateKeyValuePair(index, 'key', $event)"
                  class="flex-1 h-9"
                />
                <span class="text-muted-foreground font-medium">=</span>
                <Input
                  placeholder="键值"
                  :value="pair.value"
                  @update:model-value="updateKeyValuePair(index, 'value', $event)"
                  class="flex-1 h-9"
                />
                <Button
                  v-if="keyValuePairs.length > 1"
                  type="button"
                  variant="ghost"
                  size="icon"
                  @click="removeKeyValuePair(index)"
                  class="h-9 w-9"
                >
                  <X class="w-4 h-4" />
                </Button>
              </div>
            </div>
          </div>
          <div
            v-else-if="formData.type === 'file' || formData.type === 'image'"
            class="border-2 border-dashed rounded-lg p-8 text-center cursor-pointer hover:border-primary/50 transition-colors bg-muted/50 dark:bg-muted"
            :class="[getFieldError('content') ? 'border-destructive' : '', frozen ? 'opacity-60 pointer-events-none' : '']"
            :title="frozen ? `已冻结:${frozenReason || '该范围不可上传资源'}` : undefined"
            @click="handleFileUpload(formData.type)"
          >
            <Upload v-if="!formData.content" class="w-10 h-10 mx-auto text-muted-foreground" />
            <p v-if="!formData.content" class="text-sm text-muted-foreground mt-3">
              点击上传{{ formData.type === 'image' ? '图片' : '文件' }}
            </p>
            <img
              v-else-if="formData.type === 'image' && formData.content"
              :src="resolveAssetUrl(formData.content)"
              alt="预览"
              class="max-w-full max-h-64 mx-auto object-contain rounded-md shadow-sm"
            />
            <p v-else class="text-sm truncate">{{ formData.content }}</p>
          </div>
          <p v-if="getFieldError('content')" class="text-sm text-destructive flex items-center gap-1">
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

        <DialogFooter class="pt-4 border-t border-border">
          <Button variant="outline" @click="emit('update:open', false); resetForm()" class="h-10">
            取消
          </Button>
          <Button @click="isEditing ? handleUpdate() : handleAdd()" class="h-10" :disabled="saving || frozen">
            {{ saving ? '保存中...' : '保存' }}
          </Button>
        </DialogFooter>
      </div>
    </DialogContent>
  </Dialog>
</template>
