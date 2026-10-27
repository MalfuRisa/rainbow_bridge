<script setup lang="ts">
import type { ReleaseComparison } from '@/lib/api/release-compare'
defineProps<{ summary: ReleaseComparison['summary'] }>()
</script>
<template>
  <div class="review-summary" aria-label="差异统计">
    <div v-for="(label, key) in { added: '新增', modified: '修改', removed: '删除', unchanged: '未变' }" :key="key"><strong>{{ summary[key] }}</strong><span>{{ label }}</span></div>
  </div>
</template>
<style scoped>
.review-summary { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 16px; }
.review-summary > div { display: flex; align-items: baseline; gap: 12px; padding: 16px; background: var(--spectrum-material-soft); border: 1px solid var(--spectrum-surface-line); border-radius: 12px; }
.review-summary strong { font: 600 24px/1.3 monospace; }
.review-summary span { font-size: 12px; color: var(--muted-foreground); }
</style>
