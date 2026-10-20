<script setup lang="ts">
import SpectrumImagePreview from '@/components/spectrum/SpectrumImagePreview.vue'

defineProps<{
  isOpen: boolean
  imageUrl: string
  imageName: string
  onClose: () => void
}>()
</script>

<template>
  <SpectrumImagePreview :url="isOpen ? imageUrl : null" :name="imageName" @close="onClose" />
</template>
