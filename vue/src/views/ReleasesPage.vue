<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useScopeSelection } from '@/composables/useScopeSelection'
import SpectrumLayout from '@/components/spectrum/SpectrumLayout.vue'
import SpectrumPageHeader from '@/components/spectrum/SpectrumPageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ArrowRightLeft, ChevronDown, Eye, History, Loader2, PackagePlus, RefreshCw, ArrowUpRight } from 'lucide-vue-next'
import EnvironmentChannelSelector from '@/components/resources/EnvironmentChannelSelector.vue'
import CreateReleaseDialog from '@/components/release/CreateReleaseDialog.vue'
import ReleaseReviewDialog from '@/components/release/ReleaseReviewDialog.vue'
import FixedReleaseCompareDialog from '@/components/release/FixedReleaseCompareDialog.vue'
import type { CompareTarget } from '@/lib/api/release-compare'
import ReleaseHistoryDialog from '@/components/release/ReleaseHistoryDialog.vue'
import ReleaseDetailDialog from '@/components/release/ReleaseDetailDialog.vue'
import DeleteReleaseDialog, { type DeleteTarget } from '@/components/release/DeleteReleaseDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { displayNote } from '@/lib/operation-note'
import { useEnvironmentStore } from '@/stores/environment'
import { useFreezeStore } from '@/stores/freeze'
import { useReleaseStore } from '@/stores/release'
import { releaseRuntimeURL } from '@/lib/api/release'

const router = useRouter()
const environmentStore = useEnvironmentStore()
const freezeStore = useFreezeStore()
const releaseStore = useReleaseStore()
const auth = useAuthStore()
const deleteOpen = ref(false)
const deleteTarget = ref<DeleteTarget | null>(null)
let targetEpoch = 0
async function openDelete(version: string, snapshotId: string) {
  const epoch = ++targetEpoch
  const scope = { environmentKey: selectedEnvKey.value, pipelineKey: selectedPipelineKey.value }
  const target = { ...scope, scopeId: selectedScopeId.value, version, snapshotId, environmentName: selectedEnvironment.value?.name, pipelineName: selectedPipeline.value?.name }
  // Reopening an abandoned operation always rechecks list/active first.
  await releaseStore.refreshScope(scope.environmentKey, scope.pipelineKey)
  if (epoch !== targetEpoch || selectedEnvKey.value !== scope.environmentKey || selectedPipelineKey.value !== scope.pipelineKey) return
  const row = releaseStore.items.find(item => item.meta.snapshot_id === snapshotId)
  if (!row || !releaseStore.canDelete(row)) return
  deleteTarget.value = target
  deleteOpen.value = true
}

const { envPipelineModel, selectedEnvKey, selectedPipelineKey, selectedScopeId, initialize } = useScopeSelection(() => environmentStore.environments)
const createOpen = ref(false)
const historyOpen = ref(false)
const compareOpen = ref(false)
const detailVersion = ref('')
const detailOpen = ref(false)
const activateTarget = ref<CompareTarget | null>(null)
const activateOpen = ref(false)

onMounted(async () => {
  await environmentStore.fetchOverview()
  if (!environmentStore.error) initialize()
})

const hasScope = computed(() => Boolean(selectedEnvKey.value && selectedPipelineKey.value))

const selectedEnvironment = computed(() => environmentStore.environments.find((env) => env.key === selectedEnvKey.value))
const selectedPipeline = computed(() => selectedEnvironment.value?.pipelines?.find((p) => p.key === selectedPipelineKey.value))

const freezeStatus = computed(() => freezeStore.statusFor(selectedEnvKey.value, selectedPipelineKey.value))
const frozen = computed(() => freezeStatus.value?.state === 'locked')

watch(
  [selectedEnvKey, selectedPipelineKey, selectedScopeId],
  ([envKey, pipelineKey]) => {
    targetEpoch++
    createOpen.value = false; activateOpen.value = false; detailOpen.value = false; compareOpen.value = false
    historyOpen.value = false; deleteOpen.value = false; deleteTarget.value = null
    if (envKey && pipelineKey) {
      freezeStore.loadStatus(envKey, pipelineKey)
      releaseStore.switchScope(envKey, pipelineKey)
      router.replace({ query: { env: envKey, pipeline: pipelineKey } })
    } else {
      releaseStore.switchScope('', '')
    }
  },
  { immediate: true }
)
watch(() => auth.user?.id, () => {
  targetEpoch++
  createOpen.value = false; activateOpen.value = false; detailOpen.value = false; compareOpen.value = false
  historyOpen.value = false; deleteOpen.value = false; deleteTarget.value = null
})

const followingURL = computed(() => releaseRuntimeURL(import.meta.env.BASE_URL || '/rainbow-bridge', undefined))
const fixedExampleURL = computed(() => {
  const first = releaseStore.items[0]?.meta.version
  return releaseRuntimeURL(import.meta.env.BASE_URL || '/rainbow-bridge', first || 'v1.0')
})

const openDetail = (version: string) => {
  detailVersion.value = version
  detailOpen.value = true
}

const openActivate = (version: string) => {
  const meta = releaseStore.items.find(item => item.meta.version === version)?.meta
  if (!meta) return
  activateTarget.value = { version: meta.version, snapshot_id: meta.snapshot_id }
  activateOpen.value = true
}
function openPublish() {
  activateTarget.value = null
  activateOpen.value = true
}
function publishCompared(target: CompareTarget) {
  compareOpen.value = false
  activateTarget.value = { ...target }
  activateOpen.value = true
}
function refreshPublishing() {
  releaseStore.refreshScope(selectedEnvKey.value, selectedPipelineKey.value)
  freezeStore.loadStatus(selectedEnvKey.value, selectedPipelineKey.value)
}

const formatTime = (value: string) => {
  if (!value) return ''
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? value : time.toLocaleString()
}
</script>

<template>
  <SpectrumLayout>
    <div class="spectrum-operations spectrum-release-page">
    <!-- §2.1: active business version first; editable/frozen source second; immutable
         version catalog third. Create is primary; switch and history stay secondary. -->
    <SpectrumPageHeader eyebrow="RELEASES / FROM SNAPSHOT TO LIVE" title="版本发布" description="审阅差异，再确认生效。">
      <div v-if="hasScope" class="spectrum-release-actions">
        <Button size="sm" variant="ghost" @click="releaseStore.refreshScope(selectedEnvKey, selectedPipelineKey)"><RefreshCw class="w-4 h-4 mr-1" />刷新</Button>
        <Button size="sm" variant="outline" @click="historyOpen = true"><History class="w-4 h-4 mr-1" />操作记录</Button>
        <Button size="sm" variant="outline" :disabled="!releaseStore.items.length" @click="compareOpen = true"><ArrowRightLeft class="w-4 h-4 mr-1" />版本比较</Button>
        <Button size="sm" variant="outline" @click="createOpen = true"><PackagePlus class="w-4 h-4 mr-1" />创建版本</Button>
        <Button size="sm" @click="openPublish"><ArrowUpRight class="w-4 h-4 mr-1" />发布配置</Button>
      </div>
    </SpectrumPageHeader>
    <Card class="spectrum-panel spectrum-scope-panel">
      <CardHeader class="spectrum-panel-header">
        <CardTitle class="text-lg">工作范围</CardTitle>
      </CardHeader>
      <CardContent class="spectrum-panel-content">
        <EnvironmentChannelSelector v-model="envPipelineModel" />
      </CardContent>
    </Card>

    <section v-if="hasScope" class="spectrum-release-status" aria-label="当前发布状态">
          <div class="spectrum-release-current">
            <p class="spectrum-eyebrow">当前生效版本</p>
            <strong>{{ releaseStore.active ? releaseStore.activeVersion : releaseStore.loading ? '加载中…' : releaseStore.activeError ? '读取失败' : '尚无生效版本' }}</strong>
            <p v-if="releaseStore.active" class="spectrum-release-change">
              生效时间：{{ formatTime(releaseStore.active.changed_at) || '—' }} · {{ releaseStore.active.changed_by || '—' }}<br>
              {{ displayNote(releaseStore.active.changed_reason) }}
            </p>
          </div>
          <div class="spectrum-release-editing">
            <p class="spectrum-eyebrow">编辑区</p>
            <div class="spectrum-release-source-status"><span class="spectrum-state-pill" :class="frozen ? 'is-frozen' : 'is-idle'">{{ frozen ? '已冻结' : freezeStatus ? '可编辑' : '状态待确认' }}</span></div>
            <p>{{ frozen ? '可创建候选版本。' : freezeStatus ? '发布时可在此锁版并创建候选版本。' : '请刷新冻结状态后再创建版本。' }}</p>
          </div>
    </section>

    <Card v-if="hasScope" class="spectrum-panel spectrum-section">
      <CardHeader class="spectrum-panel-header">
        <CardTitle class="text-lg">版本列表</CardTitle>
        <CardDescription>版本内容不可修改；删除后版本号也不能复用。</CardDescription>
      </CardHeader>
      <CardContent class="spectrum-panel-content">
        <p v-if="releaseStore.error" class="text-sm text-destructive mb-3">{{ releaseStore.error }}</p>
        <p v-if="releaseStore.activeError" class="text-sm text-destructive mb-3">{{ releaseStore.activeError }}</p>
        <div v-if="releaseStore.loading && releaseStore.items.length === 0" class="flex justify-center py-10">
          <Loader2 class="w-6 h-6 animate-spin text-muted-foreground" />
        </div>
        <div v-else-if="releaseStore.items.length === 0" class="text-center py-10 text-muted-foreground text-sm">
          暂无版本，点击“发布配置”准备首个版本。
        </div>
        <template v-else>
          <div class="spectrum-table">
            <Table class="spectrum-release-table">
              <TableHeader>
                <TableRow>
                  <TableHead class="py-3 px-4 text-left text-sm">版本</TableHead>
                  <TableHead class="py-3 px-4 text-left text-sm">创建时间</TableHead>
                  <TableHead class="py-3 px-4 text-left text-sm">备注</TableHead>
                  <TableHead class="py-3 px-4 text-left text-sm">内容</TableHead>
                  <TableHead class="py-3 px-4 text-left text-sm">状态</TableHead>
                  <TableHead class="py-3 px-4 text-right text-sm">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="item in releaseStore.items" :key="item.meta.snapshot_id" :class="item.is_active ? 'is-active-version' : ''">
                  <TableCell class="spectrum-release-version font-mono"><span aria-hidden="true" />{{ item.meta.version }}</TableCell>
                  <TableCell data-label="创建时间" class="text-xs text-muted-foreground">{{ formatTime(item.meta.created_at) }}</TableCell>
                  <TableCell class="spectrum-release-reason" :title="displayNote(item.meta.reason)">{{ displayNote(item.meta.reason) }}</TableCell>
                  <TableCell class="py-3 px-4 text-xs text-muted-foreground">
                    {{ item.meta.config_count ?? 0 }} 配置 · {{ item.meta.asset_count ?? 0 }} 资源
                  </TableCell>
                  <TableCell class="py-3 px-4">
                    <span v-if="item.is_active" class="spectrum-state-pill is-active">生效中</span>
                    <Badge v-else variant="outline" class="px-2 py-0.5">未生效</Badge>
                  </TableCell>
                  <TableCell class="py-3 px-4 text-right">
                    <div class="spectrum-release-row-actions">
                      <Button variant="ghost" size="sm" @click="openDetail(item.meta.version)">
                        <Eye class="w-4 h-4 mr-1" />
                        查看
                      </Button>
                      <Button variant="ghost" size="sm" @click="openActivate(item.meta.version)">查看差异</Button>
                      <Button v-if="!item.is_active" variant="outline" size="sm" @click="openActivate(item.meta.version)">
                        <ArrowRightLeft class="w-4 h-4 mr-1" />
                        {{ releaseStore.isOlderThanActive(item.meta.version) ? '切回此版本' : '切换生效版本' }}
                      </Button>
                      <Button v-if="auth.authenticated && releaseStore.canDelete(item)" variant="ghost" size="sm" class="text-destructive" @click="openDelete(item.meta.version, item.meta.snapshot_id)">删除版本</Button>
                    </div>
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
          <div v-if="releaseStore.hasMore" class="text-center pt-4">
            <Button variant="outline" size="sm" :disabled="releaseStore.loading" @click="releaseStore.loadMore(selectedEnvKey, selectedPipelineKey)">
              {{ releaseStore.loading ? '加载中...' : '加载更多' }}
            </Button>
          </div>
        </template>
      </CardContent>
    </Card>

    <Card v-else class="spectrum-panel">
      <CardContent class="spectrum-panel-content">
        <div class="spectrum-empty"><PackagePlus :size="32" /><h3>请选择环境和渠道，查看版本</h3></div>
      </CardContent>
    </Card>

    <details class="spectrum-help">
      <summary><span>业务接入说明</span><ChevronDown :size="18" /></summary>
    <Card class="spectrum-panel">
      <CardHeader class="spectrum-panel-header">
        <CardTitle class="text-lg">运行时读取</CardTitle>
      </CardHeader>
      <CardContent class="spectrum-panel-content space-y-3 text-sm">
        <div class="rounded-md border p-3 space-y-1">
          <div class="font-medium">跟随生效版本</div>
          <pre class="text-xs bg-muted/60 rounded p-2 overflow-x-auto">GET {{ followingURL }}
Headers: x-environment: &lt;环境&gt;   x-pipeline: &lt;渠道&gt;</pre>
          <p class="text-muted-foreground text-xs">切换后，新请求读取新版本；已在途请求和应用缓存不变。</p>
        </div>
        <div class="rounded-md border p-3 space-y-1">
          <div class="font-medium">固定某版本(不跟随切换)</div>
          <pre class="text-xs bg-muted/60 rounded p-2 overflow-x-auto">GET {{ fixedExampleURL }}</pre>
        </div>
        <ul class="list-disc list-inside text-muted-foreground text-xs space-y-1">
          <li>拉取成功后应整体替换配置，避免保留已删除字段；失败时由应用决定是否使用上次成功值。</li>
          <li>响应保留 JSON 数字原文;JavaScript 原生 <span class="font-mono">JSON.parse</span> 仍可能损失大整数,相关客户端请使用无损解析。</li>
          <li>单范围最多 5000 条配置、快照 payload ≤64 MiB、去重资源 ≤500 份且单份 ≤32 MiB、资源合计 ≤100 MiB；删除版本暂不释放底层文件空间。</li>
          <li>资源缺失或损坏时读取失败，不会改读编辑区内容。</li>
        </ul>
      </CardContent>
    </Card>
    </details>
    <RouterLink v-if="hasScope" class="spectrum-quiet-link mt-6" :to="{ path: '/config', query: { env: selectedEnvKey, pipeline: selectedPipelineKey } }">返回当前范围的配置工作区 <ArrowUpRight :size="14" /></RouterLink>

    <DeleteReleaseDialog :open="deleteOpen" :target="deleteTarget" @update:open="deleteOpen = $event" />
    <FixedReleaseCompareDialog :key="`compare-${selectedScopeId}`" :open="compareOpen" :environment-key="selectedEnvKey" :pipeline-key="selectedPipelineKey" :scope-id="selectedScopeId"
      :scope-name="`${selectedEnvironment?.name || selectedEnvKey} / ${selectedPipeline?.name || selectedPipelineKey}`" :items="releaseStore.items" :has-more="releaseStore.hasMore" :list-loading="releaseStore.loading" :list-error="releaseStore.error"
      @update:open="compareOpen = $event" @publish="publishCompared" @load-more="releaseStore.loadMore(selectedEnvKey, selectedPipelineKey)" />
    <CreateReleaseDialog
      :key="`create-${selectedScopeId}`"
      :scope-id="selectedScopeId"
      :open="createOpen"
      :environment-key="selectedEnvKey"
      :pipeline-key="selectedPipelineKey"
      :environment-name="selectedEnvironment?.name"
      :pipeline-name="selectedPipeline?.name"
      @update:open="createOpen = $event"
    />
    <ReleaseReviewDialog
      :key="`activate-${selectedScopeId}`"
      :scope-id="selectedScopeId"
      :open="activateOpen"
      :environment-key="selectedEnvKey"
      :pipeline-key="selectedPipelineKey"
      :environment-name="selectedEnvironment?.name"
      :pipeline-name="selectedPipeline?.name"
      :target="activateTarget"
      @update:open="activateOpen = $event"
      @changed="refreshPublishing"
    />
    <ReleaseHistoryDialog
      :key="`history-${selectedScopeId}`"
      :open="historyOpen"
      :environment-key="selectedEnvKey"
      :pipeline-key="selectedPipelineKey"
      @update:open="historyOpen = $event"
    />
    <ReleaseDetailDialog
      :key="`detail-${selectedScopeId}`"
      :open="detailOpen"
      :environment-key="selectedEnvKey"
      :pipeline-key="selectedPipelineKey"
      :version="detailVersion"
      @update:open="detailOpen = $event"
    />
    </div>
  </SpectrumLayout>
</template>
