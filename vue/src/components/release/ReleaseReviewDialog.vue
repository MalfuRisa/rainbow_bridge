<script setup lang="ts">
import { computed } from 'vue'
import { ArrowRight, Check, GitCompareArrows, Loader2, Lock, PackagePlus } from 'lucide-vue-next'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Label } from '@/components/ui/label'
import { useReleaseReview } from '@/composables/useReleaseReview'
import type { CompareTarget } from '@/lib/api/release-compare'
import ReleaseComparisonList from './ReleaseComparisonList.vue'
import ReleaseComparisonSummary from './ReleaseComparisonSummary.vue'

// §2.1/§2.8: version transition and changes first; scope/steps second; metadata
// last. Exactly one primary action per step. Use Spectrum material surfaces,
// --border/--foreground/--spectrum-link, 8/16/24/32 spacing and existing motion.
const props = defineProps<{
  open: boolean; scopeId: string; environmentKey: string; pipelineKey: string
  environmentName?: string; pipelineName?: string; target: CompareTarget | null
}>()
const emit = defineEmits<{ 'update:open': [value: boolean]; changed: [] }>()
const flow = useReleaseReview(() => props.open, () => ({ scopeId: props.scopeId, environmentKey: props.environmentKey, pipelineKey: props.pipelineKey }), () => props.target, () => emit('changed'))
const { candidate, frozen, reason, error, busy, loading, issues, pending, done, activeVerified, actualActive, canActivate } = flow
const { result, error: compareError, loading: comparing, loadingMore, detailsLoading, detailsError, beforeConfigs, afterConfigs } = flow.comparison
const step = computed(() => done.value ? 3 : candidate.value ? 2 : 1)
const isCurrent = computed(() => activeVerified.value && actualActive.value?.snapshot_id === candidate.value?.snapshot_id)
function close(value: boolean) { if (!busy.value) emit('update:open', value) }
</script>

<template>
  <Dialog :open="open" @update:open="close">
    <DialogContent class="release-review" :close-disabled="busy" @escape-key-down="event => { if (busy) event.preventDefault() }" @pointer-down-outside="event => event.preventDefault()">
      <DialogHeader class="review-heading">
        <DialogTitle>发布审阅</DialogTitle>
        <DialogDescription>{{ environmentName || environmentKey }} / {{ pipelineName || pipelineKey }}</DialogDescription>
      </DialogHeader>
      <ol class="review-steps" aria-label="发布步骤">
        <li v-for="(label, index) in ['准备候选', '审阅差异', '确认生效']" :key="label" :class="{ 'is-current': step === index + 1, 'is-complete': step > index + 1 }" :aria-current="step === index + 1 ? 'step' : undefined">
          <span><Check v-if="step > index + 1" :size="14" /><template v-else>{{ String(index + 1).padStart(2, '0') }}</template></span>{{ label }}
        </li>
      </ol>

      <div class="review-body">
        <div v-if="pending" class="review-notice" role="status">
          <strong>{{ pending.kind === 'create' ? '创建结果尚未确认' : '切换结果尚未确认' }}</strong>
          <p>{{ pending.payload.version ? `版本 ${pending.payload.version}。` : '版本号由系统生成。' }}请先确认原请求结果，避免重复操作。</p>
          <p v-if="pending.payload.reason">原备注：{{ pending.payload.reason }}</p>
          <Button variant="outline" size="sm" :disabled="busy" @click="flow.confirmPending">确认原请求结果</Button>
        </div>
        <p v-if="error" role="alert" class="review-error">{{ error }}</p>
        <ul v-if="issues.length" class="review-error"><li v-for="(issue, index) in issues" :key="index">{{ issue.alias || issue.resource_key || issue.file_id }}：资源无法用于发布，请检查文件与引用。</li></ul>

        <section v-if="done" class="review-complete" role="status">
          <Check :size="32" />
          <h3>{{ isCurrent ? `版本 ${candidate?.version} 已生效` : '切换操作已确认' }}</h3>
          <p v-if="activeVerified && !isCurrent">{{ actualActive ? `当前生效版本为 ${actualActive.version}，请以最新状态为准。` : '当前尚无生效版本，请刷新列表核对。' }}</p>
        </section>
        <template v-else-if="!candidate">
          <div class="review-source">
            <Lock :size="20" /><div><strong>{{ loading ? '读取编辑区状态…' : frozen ? '编辑区已冻结' : flow.freeze.value ? '锁版后创建候选版本' : '编辑区状态待确认' }}</strong><p>候选版本创建后不会自动生效。退出审阅会保留候选版本和冻结状态。</p></div>
            <Button variant="ghost" size="sm" :disabled="busy || loading" @click="flow.refreshPreparation">刷新状态</Button>
          </div>
          <div class="review-form">
            <p class="text-sm text-muted-foreground">版本号由系统生成。</p>
            <div><Label for="review-reason">备注（选填）</Label><Textarea id="review-reason" v-model="reason" :disabled="busy || !!pending" :rows="2" placeholder="简述这次发布的变更" /></div>
          </div>
        </template>
        <template v-else>
          <div class="review-transition">
            <div><span>当前生效</span><strong>{{ result?.baseline?.version ?? (comparing ? '读取中…' : result ? '尚无' : '待确认') }}</strong></div>
            <ArrowRight :size="24" />
            <div><span>{{ result?.target_is_active ? '所选版本' : '候选版本' }}</span><strong>{{ candidate.version }}</strong></div>
            <span class="review-state">{{ result?.target_is_active ? '已生效' : '尚未切换' }}</span>
          </div>
          <p v-if="comparing" class="review-loading" role="status"><Loader2 :size="20" class="animate-spin" />正在比较配置…</p>
          <div v-else-if="compareError" class="review-error" role="alert">{{ compareError }} <Button variant="outline" size="sm" :disabled="busy || !!pending" @click="flow.recompare">重新比较</Button></div>
          <template v-if="result">
            <ReleaseComparisonSummary :summary="result.summary" />
            <p v-if="result.target_is_active" class="review-notice">此版本当前已生效，无需再次切换。</p>
            <p v-else-if="!result.has_changes" class="review-notice">配置内容相同，仍可切换到 {{ candidate.version }}。</p>
            <ReleaseComparisonList v-if="result.items.length" :result="result" :before="beforeConfigs" :after="afterConfigs" :loading="detailsLoading" :error="detailsError" @load="flow.comparison.loadDetails" />
            <div v-if="result.has_more" class="review-more"><span>已展示 {{ result.items.length }} / {{ result.total_changes }} 项变更</span><Button variant="outline" size="sm" :disabled="loadingMore || busy" @click="flow.comparison.more">{{ loadingMore ? '读取中…' : '加载更多差异' }}</Button></div>
            <div v-if="!result.target_is_active && !pending" class="review-note"><Label for="review-activation-note">生效备注（选填）</Label><Textarea id="review-activation-note" v-model="reason" :rows="2" :disabled="busy" placeholder="简述这次切换的原因" /></div>
          </template>
        </template>
      </div>
      <footer class="review-footer">
        <p>{{ pending ? '原请求可能已完成，关闭不会撤回操作。' : done ? '生效状态以服务端最新结果为准。' : result?.target_is_active ? '此版本当前已生效。' : candidate ? '确认后，业务新请求将读取所选版本。' : '锁版将暂停当前范围的配置与资源修改。' }}</p>
        <div><Button variant="outline" :disabled="busy" @click="close(false)">{{ pending ? '稍后确认' : done ? '完成' : result?.target_is_active ? '关闭' : '暂不发布' }}</Button>
          <Button v-if="!candidate && !pending" :disabled="busy || loading || !flow.freeze.value" @click="flow.prepare"><Loader2 v-if="busy" :size="16" class="animate-spin" /><PackagePlus v-else :size="16" />{{ busy ? '准备中…' : frozen ? '创建候选版本' : '锁版并创建候选版本' }}</Button>
          <Button v-else-if="!done && !pending && !result?.target_is_active" :disabled="!canActivate" @click="flow.activate"><Loader2 v-if="busy" :size="16" class="animate-spin" /><GitCompareArrows v-else :size="16" />{{ busy ? '正在切换…' : `确认生效 ${candidate?.version}` }}</Button>
        </div>
      </footer>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
:global(:root:has(.spectrum-shell) .workspace-dialog.release-review) { display: flex; flex-direction: column; gap: 0; width: min(1024px, calc(100vw - 64px)); max-width: none; max-height: calc(100dvh - 64px); padding: 0; overflow: hidden; }
.review-heading { padding: 24px 32px 16px; }
.review-steps { display: flex; gap: 32px; padding: 0 32px 24px; font-size: 12px; color: var(--muted-foreground); border-bottom: 1px solid var(--border); }
.review-steps li { display: flex; align-items: center; gap: 8px; }
.review-steps li > span { display: grid; place-items: center; width: 24px; height: 24px; border: 1px solid var(--border); border-radius: 8px; font-family: monospace; }
.review-steps .is-current { color: var(--foreground); font-weight: 600; }
.review-steps .is-current > span { color: var(--spectrum-link); background: var(--spectrum-material-context); border-color: var(--spectrum-link); }
.review-body { padding: 24px 32px 32px; overflow-y: auto; min-height: 0; display: grid; gap: 24px; overscroll-behavior: contain; }
.review-source { display: flex; align-items: flex-start; gap: 16px; padding: 24px; background: var(--spectrum-material-context); box-shadow: var(--spectrum-material-soft-shadow); border: 1px solid var(--spectrum-surface-line); border-radius: 12px; }
.review-source > svg { color: var(--spectrum-link); flex-shrink: 0; }
.review-source > div { flex: 1; }
.review-source strong { font-size: 14px; font-weight: 600; }
.review-source p { font-size: 12px; line-height: 1.8; color: var(--muted-foreground); margin-top: 8px; max-width: 48em; }
.review-form { display: grid; gap: 24px; }
.review-form > div, .review-note { display: grid; gap: 12px; align-content: start; }
.review-transition { display: flex; align-items: center; gap: 32px; padding: 24px; border-radius: 12px; background: var(--spectrum-material-context); box-shadow: var(--spectrum-material-soft-shadow); }
.review-transition > div { display: grid; gap: 8px; min-width: 0; }
.review-transition strong { font: 600 24px/1.3 monospace; overflow-wrap: anywhere; }
.review-transition span { font-size: 12px; color: var(--muted-foreground); }
.review-transition > svg { color: var(--spectrum-link); flex-shrink: 0; }
.review-transition .review-state { margin-left: auto; white-space: nowrap; }
.review-more > span { font-size: 12px; color: var(--muted-foreground); }
.review-notice { background: var(--spectrum-caution-bg); color: var(--spectrum-caution); padding: 16px; border-radius: 8px; font-size: 14px; line-height: 1.8; }
.review-notice button { margin-top: 12px; }
.review-error { font-size: 14px; color: var(--destructive); line-height: 1.8; overflow-wrap: anywhere; }
.review-more { display: flex; align-items: center; justify-content: center; gap: 16px; }
.review-loading, .review-complete { display: flex; justify-content: center; align-items: center; gap: 16px; padding: 32px; color: var(--muted-foreground); }
.review-complete { flex-direction: column; }
.review-complete svg { color: var(--spectrum-link); }
.review-complete h3 { font-size: 24px; font-weight: 600; color: var(--foreground); }
.review-footer { display: flex; align-items: center; justify-content: space-between; gap: 24px; padding: 24px 32px; border-top: 1px solid var(--border); background: var(--card); }
.review-footer p { max-width: 32em; font-size: 12px; color: var(--muted-foreground); line-height: 1.8; }
.review-footer > div { display: flex; gap: 12px; flex-shrink: 0; }
.review-footer button { gap: 8px; }
</style>
