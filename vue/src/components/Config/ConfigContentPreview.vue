<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ExternalLink, FileText, ImageOff } from 'lucide-vue-next'
import { resolveAssetUrl } from '@/lib/utils'
import type { ReleaseDetailConfig } from '@/lib/api/release'

const props = defineProps<{ config: Pick<ReleaseDetailConfig, 'resource_key' | 'name' | 'alias' | 'type' | 'content'> }>()
const emit = defineEmits<{ imagePreview: [content: string, name: string] }>()
const showSource = ref(false)
const imageFailed = ref(false)
const name = computed(() => props.config.name || props.config.alias)
const hasPreview = computed(() => ['image', 'color', 'file', 'richtext'].includes(props.config.type))
const validColor = computed(() => CSS.supports('color', props.config.content.trim()))
const resourceURL = computed(() => {
  const value = props.config.content.trim()
  if (!value) return ''
  const resolved = resolveAssetUrl(value)
  try {
    const url = new URL(resolved, window.location.origin)
    return ['http:', 'https:'].includes(url.protocol) ? resolved : ''
  } catch { return '' }
})

watch(() => [props.config.resource_key, props.config.type, props.config.content], () => {
  showSource.value = false
  imageFailed.value = false
})

function previewImage() {
  imageFailed.value = false
  if (resourceURL.value) emit('imagePreview', resourceURL.value, name.value)
}
</script>

<template>
  <div class="runtime-config-value">
    <template v-if="hasPreview && !showSource">
      <button v-if="config.type === 'image'" class="runtime-image-preview" :aria-label="`查看 ${name} 大图`" @click="previewImage">
        <img v-if="resourceURL && !imageFailed" :src="resourceURL" :alt="name" loading="lazy" @error="imageFailed = true">
        <span v-else class="runtime-image-error"><ImageOff :size="20" />图片加载失败，点击重试</span>
      </button>
      <div v-else-if="config.type === 'color'" class="runtime-color-preview">
        <span v-if="validColor" class="runtime-color-swatch" role="img" :aria-label="`颜色预览 ${config.content}`" :style="{ backgroundColor: config.content.trim() }" />
        <code>{{ config.content }}</code>
        <span v-if="!validColor" class="runtime-value-error">颜色值无效</span>
      </div>
      <div v-else-if="config.type === 'file'" class="runtime-file-preview">
        <FileText :size="24" aria-hidden="true" />
        <span>{{ name }}</span>
        <a v-if="resourceURL" :href="resourceURL" target="_blank" rel="noopener noreferrer" :aria-label="`打开 ${name}`">打开文件 <ExternalLink :size="14" /></a>
        <span v-else class="runtime-value-error">文件地址无效</span>
      </div>
      <iframe v-else-if="config.type === 'richtext'" class="runtime-richtext-preview" :srcdoc="config.content" sandbox="" referrerpolicy="no-referrer" :title="`${name} 富文本预览`" />
    </template>
    <!-- All raw values remain strings: previewing never parses or rewrites them. -->
    <pre v-else class="runtime-value-raw" tabindex="0">{{ config.content }}</pre>
    <button v-if="hasPreview" class="runtime-value-toggle" :aria-label="`查看 ${name} ${showSource ? '预览' : '原文'}`" :aria-pressed="showSource" @click="showSource = !showSource">{{ showSource ? '查看预览' : '查看原文' }}</button>
  </div>
</template>

<style scoped>
.runtime-image-preview { display: inline-flex; align-items: center; justify-content: center; max-width: 224px; min-height: 64px; padding: 8px; border: 1px solid var(--border); border-radius: 8px; background: var(--card); cursor: zoom-in; vertical-align: middle; }
.runtime-image-preview img { display: block; max-width: 100%; max-height: 128px; object-fit: contain; border-radius: 4px; }
.runtime-image-preview:focus-visible { outline: 2px solid var(--ring); outline-offset: 4px; }
.runtime-image-error { display: flex; align-items: center; gap: 8px; white-space: normal; color: var(--muted-foreground); font: 12px/2 'Microsoft YaHei', sans-serif; }
.runtime-config-value { display: flex; flex-direction: column; align-items: flex-start; gap: 12px; min-width: 0; white-space: normal; }
.runtime-value-raw { max-width: 100%; max-height: 192px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; margin: 0; font: inherit; }
.runtime-value-toggle { min-height: 32px; padding: 4px 0; color: var(--spectrum-link); cursor: pointer; font: 12px/2 'Microsoft YaHei', sans-serif; }
.runtime-value-toggle:hover { text-decoration: underline; text-underline-offset: 3px; }
.runtime-value-toggle:focus-visible { outline: 2px solid var(--ring); outline-offset: 4px; border-radius: 4px; }
.runtime-color-preview { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; }
.runtime-color-swatch { display: block; width: 40px; height: 40px; flex-shrink: 0; border: 1px solid var(--border); border-radius: 8px; }
.runtime-file-preview { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; overflow-wrap: anywhere; }
.runtime-file-preview > svg { color: var(--muted-foreground); flex-shrink: 0; }
.runtime-file-preview > a { display: inline-flex; align-items: center; gap: 4px; min-height: 32px; color: var(--spectrum-link); font: 12px/2 'Microsoft YaHei', sans-serif; text-decoration: underline; text-underline-offset: 3px; }
.runtime-value-error { color: var(--muted-foreground); font: 12px/2 'Microsoft YaHei', sans-serif; }
.runtime-richtext-preview { width: 100%; min-width: 0; height: 160px; border: 1px solid var(--border); border-radius: 8px; background: var(--spectrum-inverse); color-scheme: light; }
</style>
