<script setup lang="ts">
import { Badge } from '@/components/ui/badge'

type ExportFormat = 'zip' | 'tar.gz'

const { format, stats } = defineProps<{
  format: ExportFormat
  stats: {
    envCount: number
    pipeCount: number
    configCount: number
  }
}>()
</script>

<template>
  <div class="space-y-3">
    <div class="flex items-center justify-between text-sm">
      <span class="text-muted-foreground">格式</span>
      <Badge variant="outline">.{{ format }}</Badge>
    </div>
    <div class="flex items-center justify-between text-sm">
      <span class="text-muted-foreground">环境</span>
      <span>{{ stats.envCount }}</span>
    </div>
    <div class="flex items-center justify-between text-sm">
      <span class="text-muted-foreground">渠道</span>
      <span>{{ stats.pipeCount }}</span>
    </div>
    <div class="flex items-center justify-between text-sm">
      <span class="text-muted-foreground">配置项</span>
      <span>{{ stats.configCount }}</span>
    </div>
  </div>
</template>
