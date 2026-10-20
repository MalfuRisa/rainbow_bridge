<script setup lang="ts">
import { computed } from 'vue'
import { Type, AlignLeft, Code2, Hash, Binary, Braces, List, Palette, File, Image, ToggleLeft } from 'lucide-vue-next'
import type { ConfigType } from '@/lib/types'
const props = defineProps<{ type: ConfigType }>()
const icons = { text: Type, textarea: AlignLeft, richtext: Code2, number: Hash, decimal: Binary, boolean: ToggleLeft, keyvalue: List, object: Braces, color: Palette, file: File, image: Image }
const icon = computed(() => icons[props.type] || File)
</script>
<template><component :is="icon" :size="18" aria-hidden="true" /></template>
