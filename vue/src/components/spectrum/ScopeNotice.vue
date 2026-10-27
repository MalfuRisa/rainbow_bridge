<script setup lang="ts">
import { scopeProblem } from '@/lib/scope'
import { Button } from '@/components/ui/button'
function refresh() { window.location.reload() }
</script>
<template>
  <div v-if="scopeProblem" class="spectrum-scope-notice" role="alert">
    <p>{{ scopeProblem }}</p><Button variant="outline" size="sm" @click="refresh">刷新页面</Button>
  </div>
</template>
<style scoped>
.spectrum-scope-notice { display: flex; align-items: center; justify-content: space-between; gap: 24px; padding: 16px 24px; margin-bottom: 24px; border-radius: 12px; background: var(--spectrum-caution-bg); color: var(--spectrum-caution); font-size: 14px; }
</style>
