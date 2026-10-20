<script setup lang="ts">
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Check, AlertCircle } from 'lucide-vue-next'

interface Props {
  status: string
  selectedConfigs: string[]
}

interface Emits {
  (e: 'reset'): void
}

defineProps<Props>()
const emit = defineEmits<Emits>()

const handleReset = () => {
  emit('reset')
}
</script>

<template>
  <Card v-if="status === 'success' || status === 'error'" class="spectrum-panel">
    <CardContent class="spectrum-panel-content">
      <div class="spectrum-empty" :role="status === 'error' ? 'alert' : 'status'">
        <div class="spectrum-result-icon" :class="status === 'success' ? 'is-success' : 'is-error'">
          <Check v-if="status === 'success'" class="w-8 h-8" /><AlertCircle v-else class="w-8 h-8 text-destructive" />
        </div>
        <h3 class="text-lg font-medium text-foreground mb-2">
          {{ status === 'success' ? '迁移完成' : '迁移未完成，请核对结果' }}
        </h3>
        <p class="text-muted-foreground mb-6">
          <template v-if="status === 'success'">已成功迁移 {{ selectedConfigs.length }} 个配置项</template>
          <template v-else>请根据错误提示检查范围、冻结状态或网络连接；部分成功时请先核对目标内容。</template>
        </p>
        <Button @click="handleReset">开始新的迁移</Button>
      </div>
    </CardContent>
  </Card>
</template>
