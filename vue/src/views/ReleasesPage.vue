<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import SpectrumLayout from '@/components/spectrum/SpectrumLayout.vue'
import SpectrumPageHeader from '@/components/spectrum/SpectrumPageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ArrowRightLeft, ChevronDown, Eye, History, Loader2, PackagePlus, RefreshCw, ArrowUpRight } from 'lucide-vue-next'
import EnvironmentChannelSelector from '@/components/resources/EnvironmentChannelSelector.vue'
import CreateReleaseDialog from '@/components/release/CreateReleaseDialog.vue'
import ActivateReleaseDialog from '@/components/release/ActivateReleaseDialog.vue'
import ReleaseHistoryDialog from '@/components/release/ReleaseHistoryDialog.vue'
import ReleaseDetailDialog from '@/components/release/ReleaseDetailDialog.vue'
import { useEnvironmentStore } from '@/stores/environment'
import { useFreezeStore } from '@/stores/freeze'
import { useReleaseStore } from '@/stores/release'
import { releaseRuntimeURL } from '@/lib/api/release'

const route = useRoute()
const router = useRouter()
const environmentStore = useEnvironmentStore()
const freezeStore = useFreezeStore()
const releaseStore = useReleaseStore()

const envPipelineModel = ref({ envKey: '', pipelineKey: '' })
const createOpen = ref(false)
const historyOpen = ref(false)
const detailVersion = ref('')
const detailOpen = ref(false)
const activateTarget = ref('')
const activateOpen = ref(false)

onMounted(() => {
  environmentStore.fetchOverview()
  const env = typeof route.query.env === 'string' ? route.query.env : ''
  const pipeline = typeof route.query.pipeline === 'string' ? route.query.pipeline : ''
  if (env && pipeline) {
    envPipelineModel.value = { envKey: env, pipelineKey: pipeline }
  }
})

const selectedEnvKey = computed(() => envPipelineModel.value.envKey)
const selectedPipelineKey = computed(() => envPipelineModel.value.pipelineKey)
const hasScope = computed(() => Boolean(selectedEnvKey.value && selectedPipelineKey.value))

const selectedEnvironment = computed(() => environmentStore.environments.find((env) => env.key === selectedEnvKey.value))
const selectedPipeline = computed(() => selectedEnvironment.value?.pipelines?.find((p) => p.key === selectedPipelineKey.value))

const freezeStatus = computed(() => freezeStore.statusFor(selectedEnvKey.value, selectedPipelineKey.value))
const frozen = computed(() => freezeStatus.value?.state === 'locked')

watch(
  [selectedEnvKey, selectedPipelineKey],
  ([envKey, pipelineKey]) => {
    if (envKey && pipelineKey) {
      freezeStore.loadStatus(envKey, pipelineKey)
      releaseStore.switchScope(envKey, pipelineKey)
      router.replace({ query: { env: envKey, pipeline: pipelineKey } })
    } else {
      releaseStore.reset()
    }
  },
  { immediate: true }
)

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
  activateTarget.value = version
  activateOpen.value = true
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
    <SpectrumPageHeader eyebrow="RELEASES / FROM SNAPSHOT TO LIVE" title="版本发布" description="留下不可变的快照，由你决定哪一个版本正式生效。">
      <div v-if="hasScope" class="spectrum-release-actions">
        <Button size="sm" variant="ghost" @click="releaseStore.refreshScope(selectedEnvKey, selectedPipelineKey)"><RefreshCw class="w-4 h-4 mr-1" />刷新</Button>
        <Button size="sm" variant="outline" @click="historyOpen = true"><History class="w-4 h-4 mr-1" />操作记录</Button>
        <Button size="sm" @click="createOpen = true"><PackagePlus class="w-4 h-4 mr-1" />创建版本</Button>
      </div>
    </SpectrumPageHeader>
    <Card class="spectrum-panel spectrum-scope-panel">
      <CardHeader class="spectrum-panel-header">
        <CardTitle class="text-lg">工作范围</CardTitle>
        <CardDescription>版本属于一个具体范围;同范围内版本号唯一,不同范围可同名</CardDescription>
      </CardHeader>
      <CardContent class="spectrum-panel-content">
        <EnvironmentChannelSelector v-model="envPipelineModel" />
      </CardContent>
    </Card>

    <section v-if="hasScope" class="spectrum-release-status" aria-label="当前发布状态">
          <div class="spectrum-release-current">
            <p class="spectrum-eyebrow">CURRENTLY SERVING / 生效版本</p>
            <strong>{{ releaseStore.active ? releaseStore.activeVersion : releaseStore.loading ? '加载中…' : '未设置' }}</strong>
            <span v-if="releaseStore.active" class="spectrum-state-pill is-active">生效中</span>
            <p v-if="releaseStore.active?.changed_reason" class="spectrum-release-change">
              最近切换:{{ releaseStore.active.changed_reason }}({{ formatTime(releaseStore.active.changed_at) }})
            </p>
            <p v-else class="spectrum-release-change">创建版本后不会自动生效，需显式切换。</p>
            <code>head revision {{ releaseStore.active?.revision ?? '0' }}</code>
          </div>
          <div class="spectrum-release-editing">
            <p class="spectrum-eyebrow">EDITING AREA / 快照来源</p>
            <div class="spectrum-release-source-status"><span>编辑区</span><span class="spectrum-state-pill" :class="frozen ? 'is-frozen' : 'is-idle'">{{ frozen ? '已冻结' : freezeStatus ? '可编辑' : '状态待确认' }}</span><code>revision {{ freezeStatus?.revision ?? '—' }}</code></div>
            <p>{{ frozen ? '已锁定当前内容，可以保存为新版本。' : '先在配置管理中锁版，再从冻结内容创建快照。' }}</p>
          </div>
    </section>

    <Card v-if="hasScope" class="spectrum-panel spectrum-section">
      <CardHeader class="spectrum-panel-header">
        <CardTitle class="text-lg">版本列表</CardTitle>
        <CardDescription>按创建顺序倒序;版本不可修改、不可覆盖、本版不提供删除</CardDescription>
      </CardHeader>
      <CardContent class="spectrum-panel-content">
        <p v-if="releaseStore.error" class="text-sm text-destructive mb-3">{{ releaseStore.error }}</p>
        <div v-if="releaseStore.loading && releaseStore.items.length === 0" class="flex justify-center py-10">
          <Loader2 class="w-6 h-6 animate-spin text-muted-foreground" />
        </div>
        <div v-else-if="releaseStore.items.length === 0" class="text-center py-10 text-muted-foreground text-sm">
          暂无版本:先锁版,再点击“创建版本”
        </div>
        <template v-else>
          <div class="spectrum-table">
            <Table class="spectrum-release-table">
              <TableHeader>
                <TableRow>
                  <TableHead class="py-3 px-4 text-left text-sm">版本</TableHead>
                  <TableHead class="py-3 px-4 text-left text-sm">来源冻结</TableHead>
                  <TableHead class="py-3 px-4 text-left text-sm">创建时间</TableHead>
                  <TableHead class="py-3 px-4 text-left text-sm">说明</TableHead>
                  <TableHead class="py-3 px-4 text-left text-sm">内容</TableHead>
                  <TableHead class="py-3 px-4 text-left text-sm">状态</TableHead>
                  <TableHead class="py-3 px-4 text-right text-sm">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="item in releaseStore.items" :key="item.meta.snapshot_id" :class="item.is_active ? 'is-active-version' : ''">
                  <TableCell class="spectrum-release-version font-mono"><span aria-hidden="true" />{{ item.meta.version }}</TableCell>
                  <TableCell data-label="来源冻结" class="text-sm font-mono">rev {{ item.meta.source_freeze_revision }}</TableCell>
                  <TableCell data-label="创建时间" class="text-xs text-muted-foreground">{{ formatTime(item.meta.created_at) }}</TableCell>
                  <TableCell class="spectrum-release-reason" :title="item.meta.reason">{{ item.meta.reason }}</TableCell>
                  <TableCell class="py-3 px-4 text-xs text-muted-foreground">
                    {{ item.meta.config_count ?? 0 }} 配置 · {{ item.meta.asset_count ?? 0 }} 资源
                  </TableCell>
                  <TableCell class="py-3 px-4">
                    <span v-if="item.is_active" class="spectrum-state-pill is-active">生效中</span>
                    <Badge v-else variant="outline" class="px-2 py-0.5">未生效</Badge>
                  </TableCell>
                  <TableCell class="py-3 px-4 text-right">
                    <div class="flex items-center justify-end gap-2">
                      <Button variant="ghost" size="sm" @click="openDetail(item.meta.version)">
                        <Eye class="w-4 h-4 mr-1" />
                        查看
                      </Button>
                      <Button v-if="!item.is_active" variant="outline" size="sm" @click="openActivate(item.meta.version)">
                        <ArrowRightLeft class="w-4 h-4 mr-1" />
                        {{ releaseStore.isOlderThanActive(item.meta.version) ? '切回此版本' : '切换生效版本' }}
                      </Button>
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
        <div class="spectrum-empty"><PackagePlus :size="32" /><h3>先确定发布范围</h3><p>
        请先选择环境和渠道以查看发布版本
        </p></div>
      </CardContent>
    </Card>

    <details class="spectrum-help" open>
      <summary><span>业务接入说明</span><ChevronDown :size="18" /></summary>
    <Card class="spectrum-panel">
      <CardHeader class="spectrum-panel-header">
        <CardTitle class="text-lg">运行时读取</CardTitle>
        <CardDescription>两类读取地址,选择一种接入方式</CardDescription>
      </CardHeader>
      <CardContent class="spectrum-panel-content space-y-3 text-sm">
        <div class="rounded-md border p-3 space-y-1">
          <div class="font-medium">跟随生效版本(推荐,配合切换/切回)</div>
          <pre class="text-xs bg-muted/60 rounded p-2 overflow-x-auto">GET {{ followingURL }}
Headers: x-environment: &lt;环境&gt;   x-pipeline: &lt;渠道&gt;</pre>
          <p class="text-muted-foreground text-xs">切换生效版本后,随后开始的新请求读取新版本;已在途请求与业务本地缓存不会被服务端撤回。</p>
        </div>
        <div class="rounded-md border p-3 space-y-1">
          <div class="font-medium">固定某版本(不跟随切换)</div>
          <pre class="text-xs bg-muted/60 rounded p-2 overflow-x-auto">GET {{ fixedExampleURL }}</pre>
        </div>
        <ul class="list-disc list-inside text-muted-foreground text-xs space-y-1">
          <li>旧接口 <span class="font-mono">/api/v1/runtime/config</span> 仍读取编辑区,默认语义不变;需要版本化请改接上面的新地址。</li>
          <li>建议业务每 30 秒左右完整重读一次并整体替换本地一套值,不要只合并新字段;失败时由应用决定是否沿用上次成功值。</li>
          <li>响应保留 JSON 数字原文;JavaScript 原生 <span class="font-mono">JSON.parse</span> 仍可能损失大整数,相关客户端请使用无损解析。</li>
          <li>初版边界:单范围最多 5000 条配置、快照 payload ≤64 MiB、去重资源 ≤500 份且单份 ≤32 MiB、资源合计 ≤100 MiB;版本不可删除,磁盘会随版本积累。</li>
          <li>切换目标会校验该版本全部资源可读且 SHA-256 一致;已提交版本数据缺失时读取返回 503,不会回退编辑区。</li>
        </ul>
      </CardContent>
    </Card>
    </details>
    <RouterLink v-if="hasScope" class="spectrum-quiet-link mt-6" :to="{ path: '/config', query: { env: selectedEnvKey, pipeline: selectedPipelineKey } }">返回当前范围的配置工作区 <ArrowUpRight :size="14" /></RouterLink>

    <CreateReleaseDialog
      :open="createOpen"
      :environment-key="selectedEnvKey"
      :pipeline-key="selectedPipelineKey"
      :environment-name="selectedEnvironment?.name"
      :pipeline-name="selectedPipeline?.name"
      @update:open="createOpen = $event"
    />
    <ActivateReleaseDialog
      :open="activateOpen"
      :environment-key="selectedEnvKey"
      :pipeline-key="selectedPipelineKey"
      :target-version="activateTarget"
      @update:open="activateOpen = $event"
    />
    <ReleaseHistoryDialog
      :open="historyOpen"
      :environment-key="selectedEnvKey"
      :pipeline-key="selectedPipelineKey"
      @update:open="historyOpen = $event"
    />
    <ReleaseDetailDialog
      :open="detailOpen"
      :environment-key="selectedEnvKey"
      :pipeline-key="selectedPipelineKey"
      :version="detailVersion"
      @update:open="detailOpen = $event"
    />
    </div>
  </SpectrumLayout>
</template>
