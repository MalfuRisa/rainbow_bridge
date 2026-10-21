<script setup lang="ts">
import { computed } from 'vue'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Checkbox } from '@/components/ui/checkbox'
import { Badge } from '@/components/ui/badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { AlertCircle, Loader2 } from 'lucide-vue-next'
import type { MigrationConfig } from '@/views/MigrationPage.vue'

interface Props {
  previewData: MigrationConfig[]
  selectedConfigs: string[]
  overwriteConflicts: boolean
  status: string
}

interface Emits {
  (e: 'update:selectedConfigs', value: string[]): void
  (e: 'update:overwriteConflicts', value: boolean): void
  (e: 'reset'): void
  (e: 'migrate'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const conflictCount = computed(() =>
  props.previewData.filter((p) => p.hasConflict && props.selectedConfigs.includes(p.id)).length
)

const newCount = computed(() =>
  props.previewData.filter((p) => p.isNew && props.selectedConfigs.includes(p.id)).length
)

const toggleConfig = (id: string) => {
  if (props.selectedConfigs.includes(id)) {
    emit('update:selectedConfigs', props.selectedConfigs.filter((c) => c !== id))
  } else {
    emit('update:selectedConfigs', [...props.selectedConfigs, id])
  }
}

const toggleAll = () => {
  if (props.selectedConfigs.length === props.previewData.length) {
    emit('update:selectedConfigs', [])
  } else {
    emit('update:selectedConfigs', props.previewData.map((p) => p.id))
  }
}

const handleReset = () => {
  emit('reset')
}

const handleMigrate = () => {
  emit('migrate')
}
</script>

<template>
  <Card v-if="previewData.length > 0" class="spectrum-panel spectrum-section">
    <CardHeader class="spectrum-panel-header">
      <div class="flex items-center justify-between">
        <div>
          <CardTitle class="text-base">变更预览</CardTitle>
          <CardDescription>
            共 {{ previewData.length }} 个配置项，已选择 {{ selectedConfigs.length }} 个
            <span v-if="newCount > 0">，{{ newCount }} 个新增</span>
            <span v-if="conflictCount > 0">，{{ conflictCount }} 个存在冲突</span>
          </CardDescription>
        </div>
        <Button variant="outline" size="sm" :disabled="status === 'migrating'" @click="toggleAll" class="bg-transparent">
          {{ selectedConfigs.length === previewData.length ? '取消全选' : '全选' }}
        </Button>
      </div>
    </CardHeader>
    <CardContent class="spectrum-panel-content">
      <div class="spectrum-table">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead class="w-12" />
              <TableHead>配置名</TableHead>
              <TableHead>别名</TableHead>
              <TableHead>类型</TableHead>
              <TableHead>状态</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow
              v-for="config in previewData"
              :key="config.id"
              :class="config.hasConflict ? 'spectrum-conflict-row' : ''"
            >
              <TableCell>
                <Checkbox
                  :model-value="selectedConfigs.includes(config.id)"
                  :aria-label="`选择配置 ${config.name}`"
                  :disabled="status === 'migrating'"
                  @update:model-value="() => toggleConfig(config.id)"
                />
              </TableCell>
              <TableCell class="font-mono text-sm">{{ config.name }}</TableCell>
              <TableCell>{{ config.alias }}</TableCell>
              <TableCell>
                <Badge variant="secondary">{{ config.type }}</Badge>
              </TableCell>
              <TableCell>
                <Badge v-if="config.isNew" variant="outline" class="spectrum-state-pill is-active">
                  新增
                </Badge>
                <Badge v-else-if="config.hasConflict" variant="outline" class="spectrum-state-pill is-frozen">
                  值不同
                </Badge>
                <Badge v-else variant="outline" class="spectrum-state-pill is-idle">
                  无变更
                </Badge>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>

      <div class="mt-4 p-4 bg-muted/50 rounded-lg space-y-3">
        <div class="flex items-center gap-2">
          <Checkbox
            id="overwrite"
            :disabled="status === 'migrating'"
            :model-value="overwriteConflicts"
            @update:model-value="(v: boolean) => emit('update:overwriteConflicts', v)"
          />
          <label for="overwrite" class="text-sm text-foreground cursor-pointer">
            覆盖目标中的冲突配置
          </label>
        </div>
      </div>

      <Alert v-if="conflictCount > 0 && !overwriteConflicts" variant="destructive" class="mt-4">
        <AlertCircle class="h-4 w-4" />
        <AlertTitle>存在冲突</AlertTitle>
        <AlertDescription>
          {{ conflictCount }} 项已选配置与目标值不同。
          勾选“覆盖目标中的冲突配置”，或取消选择冲突项。
        </AlertDescription>
      </Alert>

      <div class="mt-6 flex items-center justify-between">
        <Button variant="outline" :disabled="status === 'migrating'" @click="handleReset" class="bg-transparent">
          重置
        </Button>
        <Button
          @click="handleMigrate"
          :disabled="selectedConfigs.length === 0 || status === 'migrating' || (conflictCount > 0 && !overwriteConflicts)"
        >
          <Loader2 v-if="status === 'migrating'" class="w-4 h-4 mr-2 animate-spin" />
          {{ status === 'migrating' ? '迁移中...' : `迁移 ${selectedConfigs.length} 个配置` }}
        </Button>
      </div>
    </CardContent>
  </Card>
</template>
