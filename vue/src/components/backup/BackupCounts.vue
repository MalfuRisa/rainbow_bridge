<script setup lang="ts">
import type { BackupCounts } from '@/lib/api/backup'
defineProps<{ counts: BackupCounts }>()
const labels: Array<[keyof BackupCounts, string]> = [
  ['environments', '环境'], ['pipelines', '渠道'], ['configs', '配置'], ['assets', '资源'], ['versions', '版本'],
  ['deleted_versions', '已删除版本'], ['freeze_events', '锁版记录'], ['release_events', '发布记录'], ['configs_deleted', '已删除配置'], ['assets_deleted', '已删除资源'],
]
</script>
<template>
  <dl class="backup-counts"><div v-for="[key, label] in labels" :key="key"><dt>{{ label }}</dt><dd>{{ counts[key] ?? 0 }}</dd></div></dl>
</template>
