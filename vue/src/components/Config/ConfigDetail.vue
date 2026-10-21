<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Check, Copy, ExternalLink, FileImage, FileText, LockKeyhole, Pencil } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { toast } from 'vue-sonner'
import { CONFIG_TYPE_META, type ConfigItem } from '@/lib/types'
import { resolveAssetUrl } from '@/lib/utils'

const props = defineProps<{ config: ConfigItem; frozen?: boolean }>()
const emit = defineEmits<{ edit: [config: ConfigItem]; imagePreview: [url: string, name: string] }>()
const showSource = ref(false)
const imageFailed = ref(false)
const copied = ref(false)
const typeLabel = computed(() => CONFIG_TYPE_META[props.config.type]?.label || props.config.type)
watch(() => props.config, () => { showSource.value = false; imageFailed.value = false; copied.value = false })

async function copyContent() {
  try {
    // No JSON round-trip: integers and objects retain their stored precision.
    await navigator.clipboard.writeText(props.config.content)
    copied.value = true
    toast.success('已复制配置原文')
  } catch { toast.error('复制失败，请选择原文后手动复制') }
}
</script>

<template>
  <section class="config-detail" aria-label="配置内容详情">
    <div class="config-detail-overline"><span>CONFIGURATION DETAIL</span><FileText :size="16" aria-hidden="true" /></div>
    <h2>{{ config.name }}</h2>
    <div class="config-detail-meta"><code>{{ config.alias }}</code><span>{{ typeLabel }}</span></div>
    <div class="config-content-toolbar"><span>{{ showSource ? '内容原文' : '配置内容' }}</span><div>
      <button v-if="['image', 'color', 'richtext', 'file'].includes(config.type)" class="config-detail-quiet" :aria-pressed="showSource" @click="showSource = !showSource">{{ showSource ? '查看预览' : '查看原文' }}</button>
      <button class="config-detail-quiet" aria-label="复制配置原文" @click="copyContent"><Check v-if="copied" :size="14" /><Copy v-else :size="14" />{{ copied ? '已复制' : '复制' }}</button>
    </div></div>
    <div class="config-detail-content">
      <button v-if="config.type === 'image' && !showSource" class="config-image-button" aria-label="查看配置图片大图" @click="emit('imagePreview', config.content, config.name)">
        <img v-if="!imageFailed" :src="resolveAssetUrl(config.content)" :alt="config.name" @error="imageFailed = true">
        <span v-else class="config-media-error"><FileImage :size="24" />图片加载失败，点击重试</span>
        <span class="config-media-caption">点击查看大图</span>
      </button>
      <div v-else-if="config.type === 'color' && !showSource" class="config-color-preview"><span :style="{ backgroundColor: config.content }" /><code>{{ config.content }}</code></div>
      <div v-else-if="config.type === 'file' && !showSource" class="config-file-preview"><FileText :size="32" /><code>{{ config.content }}</code><a :href="resolveAssetUrl(config.content)" target="_blank" rel="noopener noreferrer">打开文件 <ExternalLink :size="14" /></a></div>
      <iframe v-else-if="config.type === 'richtext' && !showSource" class="config-richtext-preview" :srcdoc="config.content" sandbox="" referrerpolicy="no-referrer" :title="`${config.name} 富文本预览`" />
      <pre v-else tabindex="0">{{ config.content }}</pre>
    </div>
    <div v-if="config.description" class="config-detail-description"><span>描述</span><p>{{ config.description }}</p></div>
    <div class="config-detail-bottom"><span v-if="frozen"><LockKeyhole :size="14" />已冻结 · 内容只读</span><Button variant="outline" size="sm" class="ml-auto" :disabled="frozen" @click="emit('edit', config)"><Pencil :size="14" class="mr-2" />编辑配置</Button></div>
  </section>
</template>
