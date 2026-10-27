<script setup lang="ts">
defineProps<{ eyebrow: string; title: string; description?: string }>()
</script>

<template>
  <!-- §2.1: page task first, supporting scope/count second; one primary action.
       Shared scale: title 36/30, body 14, metadata 12; spacing 12/24/32. -->
  <div class="spectrum-page-heading">
    <div>
      <p class="spectrum-eyebrow">{{ eyebrow }}</p>
      <h1>{{ title }}</h1>
      <p v-if="description" class="spectrum-page-description">{{ description }}</p>
    </div>
    <div v-if="$slots.default" class="spectrum-page-heading-aside"><slot /></div>
  </div>
</template>
