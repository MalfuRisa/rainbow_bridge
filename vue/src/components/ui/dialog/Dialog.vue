<script setup lang="ts">
import { DialogRoot, type DialogRootEmits, type DialogRootProps } from 'radix-vue'

// Preserve Radix's modal default through Vue's Boolean prop casting. Without
// this, an omitted `modal` becomes false and drops the overlay and focus trap.
const props = withDefaults(defineProps<DialogRootProps>(), { modal: true })
const emit = defineEmits<DialogRootEmits>()
</script>

<template>
  <DialogRoot v-bind="props" @update:open="emit('update:open', $event)">
    <slot />
  </DialogRoot>
</template>
