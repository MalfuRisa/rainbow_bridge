<script setup lang="ts">
import { Loader2, Check, CircleAlert, Clock3, RotateCw } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import type { BackupJob } from '@/lib/api/backup'
import { backupError, backupTime, inProgress, jobPhase, jobTitle } from './presentation'
import { ApiError } from '@/lib/api/error'
defineProps<{ jobs: BackupJob[]; busy: boolean; uncertain: boolean; error: string }>()
defineEmits<{ refresh: []; cancel: [] }>()
function jobError(job: BackupJob) {
  const error = new ApiError(500, job.error?.msg || '任务失败，请检查备份服务')
  error.errorType = job.error?.error_type || ''
  return backupError(error)
}
</script>
<template>
  <section v-if="jobs.length || error" class="backup-tasks" aria-label="备份恢复任务">
    <p v-if="error" class="backup-alert" role="alert">任务状态暂未确认：{{ error }} <Button variant="outline" @click="$emit('refresh')">重新查询任务</Button></p>
    <article v-for="job in jobs" :key="job.job_id" class="backup-task" :class="{ 'is-waiting': job.state === 'restart_required', 'is-failed': ['failed','interrupted'].includes(job.state) }">
      <div class="backup-task-heading">
        <Loader2 v-if="inProgress(job)" :size="20" class="animate-spin" />
        <Clock3 v-else-if="job.state === 'restart_required'" :size="20" />
        <Check v-else-if="job.state === 'succeeded'" :size="20" />
        <CircleAlert v-else :size="20" />
        <h2>{{ jobTitle(job) }}</h2><span>{{ backupTime(job.updated_at) }}</span>
      </div>
      <p v-if="job.error || job.state === 'failed'" role="alert">{{ jobError(job) }}</p>
      <p v-else-if="job.state === 'interrupted'">服务曾中断，任务未完成。请核对当前数据后再开始新的操作。</p>
      <p v-else>{{ jobPhase(job) }}</p>
      <div v-if="job.state === 'restart_required'" class="backup-restart">
        <p>请联系部署人员重启服务。等待期间业务写入暂停，读取仍为原数据；重启后使用当前账号重新登录，再确认恢复结果。</p>
        <div class="backup-actions"><Button variant="outline" @click="$emit('refresh')"><RotateCw :size="16" />查询恢复结果</Button><Button variant="outline" :disabled="busy || uncertain" @click="$emit('cancel')">取消待恢复计划</Button></div>
      </div>
      <details class="backup-task-reference"><summary>任务编号</summary><code>{{ job.job_id }}</code></details>
    </article>
  </section>
</template>
