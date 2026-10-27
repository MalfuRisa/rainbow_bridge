<script setup lang="ts">
import SpectrumImagePreview from '@/components/spectrum/SpectrumImagePreview.vue'
defineProps<{ url: string | null; name: string }>()
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <SpectrumImagePreview :url="url" :name="name" @close="emit('close')" />
</template>
