<script setup lang="ts">
import { ref, watch } from 'vue'
import { Loader2, ImageOff } from 'lucide-vue-next'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
const props = defineProps<{ url: string | null; name: string }>()
const emit = defineEmits<{ close: [] }>()
const loading = ref(true)
const failed = ref(false)
watch(() => props.url, () => { loading.value = true; failed.value = false })
</script>

<template>
  <Dialog :open="Boolean(url)" @update:open="(open) => { if (!open) emit('close') }">
    <DialogContent class="spectrum-image-dialog">
      <DialogHeader><DialogTitle>{{ name || '图片预览' }}</DialogTitle><DialogDescription>查看图片资源原图</DialogDescription></DialogHeader>
      <div class="spectrum-image-stage">
        <!-- Keep the image mounted during loading so load/error events can fire. -->
        <img v-if="url && !failed" :src="url" :alt="name" :class="{ 'is-loading': loading }" @load="loading = false" @error="failed = true; loading = false">
        <p v-if="loading" role="status"><Loader2 :size="24" class="animate-spin" />图片加载中…</p>
        <p v-else-if="failed" role="alert"><ImageOff :size="24" />图片加载失败，请检查资源地址</p>
      </div>
    </DialogContent>
  </Dialog>
</template>
