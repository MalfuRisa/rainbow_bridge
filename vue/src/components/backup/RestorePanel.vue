<script setup lang="ts">
import { Upload, ShieldCheck, Loader2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import type { RestorePreview } from '@/lib/api/backup'
import BackupCounts from './BackupCounts.vue'
import { backupSize, backupTime, previewNotice } from './presentation'
defineProps<{ preview: RestorePreview | null; file: File | null; canOperate: boolean; canRestore: boolean; submitting: boolean; maxSize: number; now: number; retryUpload: boolean; retentionNotice?: string }>()
const emit = defineEmits<{ select: [file?: File]; upload: []; restore: [] }>()
function select(event: Event) { const input = event.target as HTMLInputElement; emit('select', input.files?.[0]); input.value = '' }
</script>
<template>
  <section class="spectrum-panel backup-restore-panel" aria-labelledby="backup-restore-title">
    <div class="backup-section-heading"><div><p class="spectrum-eyebrow">RESTORE POINT</p><h2 id="backup-restore-title">从备份恢复</h2></div><ShieldCheck :size="24" /></div>
    <p class="backup-secondary">上传可信的虹桥业务备份 ZIP，先校验，再确认覆盖。版本静态包和导入导出包不能用于整实例恢复。</p>
    <p v-if="retentionNotice" class="backup-secondary mt-3" data-testid="rollback-retention">{{ retentionNotice }}</p>
    <label class="backup-upload" :class="{ 'is-disabled': !canOperate && !retryUpload }"><Upload :size="24" /><span>{{ file ? file.name : retryUpload ? '重新选择原备份包' : '选择业务备份包' }}</span><small>{{ file ? backupSize(file.size) : maxSize ? `ZIP · 最大 ${backupSize(maxSize)}` : 'ZIP' }}</small><input type="file" accept=".zip,application/zip" aria-label="业务备份包" :disabled="(!canOperate && !retryUpload) || submitting" @change="select" /></label>
    <Button v-if="file && !retryUpload" variant="outline" class="backup-upload-button" :disabled="!canOperate || submitting" @click="$emit('upload')"><Loader2 v-if="submitting" :size="16" class="animate-spin" />上传并校验</Button>
    <div v-if="preview" class="backup-preview">
      <div class="backup-preview-heading"><h3>恢复预览</h3><span class="spectrum-state-pill" :class="preview.valid && !preview.issues?.length ? 'is-active' : 'is-frozen'">{{ preview.valid && !preview.issues?.length ? '校验通过' : '校验未通过' }}</span></div>
      <p v-if="preview.backup" class="backup-secondary">备份时间：{{ backupTime(preview.backup.consistent_at) }}</p>
      <BackupCounts v-if="preview.backup" :counts="preview.backup.counts" />
      <div v-if="preview.warnings?.length" class="backup-alert"><h4>恢复前请留意</h4><ul><li v-for="(warning, index) in preview.warnings" :key="index">{{ previewNotice(warning) }}</li></ul></div>
      <div v-if="preview.issues?.length" class="backup-alert is-danger" role="alert"><h4>以下问题阻止恢复</h4><ul><li v-for="(issue, index) in preview.issues" :key="index">{{ previewNotice(issue) }}</li></ul></div>
      <p v-if="Date.parse(preview.expires_at) <= now" class="backup-danger-text" role="alert">预览已过期，请重新上传备份包。</p>
      <p v-else class="backup-secondary">预览有效至 {{ backupTime(preview.expires_at) }}</p>
      <Button variant="outline" class="backup-restore-button" :disabled="!canRestore" @click="$emit('restore')">核对并恢复</Button>
    </div>
  </section>
</template>
