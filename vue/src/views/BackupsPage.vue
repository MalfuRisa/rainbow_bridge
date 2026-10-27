<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { RefreshCw, ShieldCheck, Loader2 } from 'lucide-vue-next'
import SpectrumLayout from '@/components/spectrum/SpectrumLayout.vue'
import BackupList from '@/components/backup/BackupList.vue'
import RestorePanel from '@/components/backup/RestorePanel.vue'
import BackupJobs from '@/components/backup/BackupJobs.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { useBackups } from '@/composables/useBackups'
import type { BackupItem } from '@/lib/api/backup'
import { backupTime, supportReason, rollbackRetentionNotice } from '@/components/backup/presentation'
import '@/styles/spectrum-backup.css'

const backup = reactive(useBackups())
const retentionNotice = computed(() => rollbackRetentionNotice(backup.capabilities))
const modal = ref<'create' | 'restore' | 'delete' | 'cancel' | null>(null)
const reason = ref('')
const acknowledged = ref(false)
const deleting = ref<BackupItem | null>(null)
function open(value: typeof modal.value, item?: BackupItem) { reason.value = ''; acknowledged.value = false; deleting.value = item || null; modal.value = value }
function close(value: boolean) { if (!value && !backup.submitting && !backup.pending) modal.value = null }
async function confirm() {
  let success = false
  if (modal.value === 'create') success = await backup.create(reason.value)
  if (modal.value === 'restore' && acknowledged.value) success = await backup.execute(reason.value)
  if (modal.value === 'delete' && deleting.value) success = await backup.remove(deleting.value)
  if (modal.value === 'cancel') success = await backup.cancelRestore()
  if (success) modal.value = null
}
async function retry() { if (await backup.sendPending()) modal.value = null }
</script>

<template>
  <!-- §2.1 / §3.6 / §6.1: backup list is primary; upload/preview secondary;
       destructive restore requires one explicit review. Reuse Spectrum materials,
       16/24/32 spacing, existing dialogs and semantic warning colors. -->
  <SpectrumLayout>
    <div class="spectrum-operations spectrum-backup-page">
      <header class="spectrum-page-heading"><div><p class="spectrum-eyebrow">BACKUP / RECOVERY</p><h1>备份与恢复</h1><p class="spectrum-page-description">为整个实例保留恢复点。</p></div><Button variant="outline" :disabled="backup.loading || backup.listLoading" @click="backup.refresh"><RefreshCw :size="16" />刷新</Button></header>
      <section class="backup-overview"><ShieldCheck :size="24" /><div><h2>业务数据一起保存，恢复前先核对</h2><p>包含配置、资源、锁版状态、发布版本与历史。保留当前管理员账号；备份包不含登录凭据和部署配置。</p></div><span class="spectrum-state-pill" :class="backup.capabilities?.supported ? 'is-active' : 'is-idle'">{{ backup.loading ? '确认能力中' : backup.capabilities?.supported ? '整实例备份' : '暂不可用' }}</span></section>
      <div v-if="backup.loading && !backup.capabilities" class="backup-empty" role="status"><Loader2 class="animate-spin" />正在确认当前部署</div>
      <div v-if="backup.error && !modal" class="backup-alert is-danger" role="alert">{{ backup.error }}</div>
      <div v-if="backup.storageError" class="backup-alert is-danger" role="alert">{{ backup.storageError }}</div>
      <section v-if="backup.capabilities && !backup.capabilities.supported" class="backup-alert"><h2>当前部署暂不支持整实例备份</h2><ul><li v-for="code in backup.capabilities.reasons" :key="code">{{ supportReason(code) }}</li></ul><p>第一版支持 SQLite、本地资源、Redis 关闭的单实例部署，数据与控制目录需满足服务端要求。</p></section>
      <div v-if="backup.pending && !modal" class="backup-alert" role="status"><h2>上次操作结果尚未确认</h2><p>请保留原请求，确认后再开始其他操作。{{ backup.pending.action === 'restore/preview' ? '若离开过页面，请重新选择原备份包。' : '' }}</p><Button variant="outline" :disabled="backup.submitting" @click="retry">{{ backup.submitting ? '确认中…' : '确认上次请求结果' }}</Button></div>
      <BackupJobs :jobs="backup.jobs" :busy="backup.submitting" :uncertain="!!backup.pending || backup.unresolved" :error="backup.taskError" @refresh="backup.poll" @cancel="open('cancel')" />
      <div v-if="backup.capabilities" class="backup-layout">
        <BackupList :items="backup.items" :loading="backup.listLoading" :can-operate="backup.canOperate" :downloading="backup.downloading" :has-more="backup.hasMore" :supported="backup.capabilities.supported" @create="open('create')" @download="backup.download" @remove="item => open('delete', item)" @more="backup.loadList(true)" />
        <RestorePanel :retention-notice="retentionNotice" :preview="backup.preview" :file="backup.file" :can-operate="backup.canOperate" :can-restore="backup.canRestore" :submitting="backup.submitting" :max-size="backup.capabilities.limits.package_bytes" :now="backup.now" :retry-upload="backup.pending?.action === 'restore/preview'" @select="backup.selectFile" @upload="backup.upload" @restore="open('restore')" />
      </div>
      <details class="spectrum-help"><summary>备份与恢复须知</summary><div class="backup-help"><p>备份包可能包含业务敏感配置，请妥善保存。仅恢复来自可信来源的业务备份。</p><p>恢复覆盖全部业务数据，需要部署人员重启服务。完成后旧会话失效，请用当前账号重新登录。</p><p v-if="!retentionNotice">恢复前的数据副本按当前部署策略保留；回退需由部署人员处理。</p><p v-if="backup.capabilities">服务器最多保留 {{ backup.capabilities.limits.max_backups }} 份可下载的备份包。重要备份请下载另存。</p></div></details>
    </div>
    <Dialog :open="!!modal" @update:open="close">
      <DialogContent :close-disabled="backup.submitting" class="backup-confirm-dialog" @pointer-down-outside.prevent @escape-key-down="event => { if (backup.submitting) event.preventDefault() }">
        <DialogHeader><DialogTitle>{{ modal === 'create' ? '创建业务备份' : modal === 'restore' ? '确认恢复全部业务数据' : modal === 'cancel' ? '取消待恢复计划' : '删除业务备份' }}</DialogTitle><DialogDescription>{{ modal === 'create' ? '保存此刻的完整业务数据，复制期间短暂暂停业务写入。' : modal === 'restore' ? '确认前请核对备份时间和预览内容。' : modal === 'cancel' ? '放弃本次恢复准备，保留当前业务数据并解除写入暂停。' : '删除后无法从服务器下载此备份，不会删除当前业务数据。' }}</DialogDescription></DialogHeader>
        <div v-if="modal === 'restore'" class="backup-confirm-risk"><strong>恢复至 {{ backupTime(backup.preview?.backup?.consistent_at) }}</strong><ul><li>覆盖全部配置、资源、发布版本及历史。</li><li>保留当前管理员账号和密码。</li><li>准备后暂停业务写入，需部署人员重启服务。</li><li>恢复后旧会话失效，需要重新登录。</li></ul><label class="backup-acknowledgement"><input v-model="acknowledged" type="checkbox" :disabled="backup.submitting || !!backup.pending" />我已核对预览，确认覆盖全部业务数据</label></div>
        <p v-if="modal === 'delete' && deleting" class="backup-secondary">{{ backupTime(deleting.consistent_at) }} · {{ deleting.reason || '未填写备注' }}</p>
        <div v-if="modal === 'create' || modal === 'restore'" class="backup-reason"><Label for="backup-reason">备注（选填）</Label><Input id="backup-reason" v-model="reason" maxlength="500" :disabled="backup.submitting || !!backup.pending" placeholder="例如：升级前备份" /></div>
        <p v-if="backup.error" class="backup-danger-text" role="alert">{{ backup.error }}</p>
        <div v-if="backup.pending" class="backup-alert"><p>操作结果尚未确认，重试将使用同一个请求。</p><Button variant="outline" :disabled="backup.submitting" @click="retry">确认上次请求结果</Button></div>
        <DialogFooter><Button variant="outline" :disabled="backup.submitting" @click="modal = null">{{ backup.pending ? '稍后确认' : '取消' }}</Button><Button v-if="!backup.pending" :variant="modal === 'restore' || modal === 'delete' ? 'destructive' : 'default'" :disabled="backup.submitting || (modal === 'restore' && (!acknowledged || !backup.canRestore))" @click="confirm">{{ backup.submitting ? '提交中…' : modal === 'create' ? '开始备份' : modal === 'restore' ? '确认覆盖并准备恢复' : modal === 'cancel' ? '取消恢复计划' : '确认删除备份' }}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  </SpectrumLayout>
</template>
