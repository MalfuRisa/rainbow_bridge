<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import SpectrumLayout from '@/components/spectrum/SpectrumLayout.vue'
import SpectrumPageHeader from '@/components/spectrum/SpectrumPageHeader.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogTrigger, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Plus, Trash2, GitBranch, ArrowUpRight, Layers, Loader2, Lock } from 'lucide-vue-next'
import { useEnvironmentStore } from '@/stores/environment'
import { useFreezeStore } from '@/stores/freeze'
import type { Environment } from '@/lib/types'

const environmentStore = useEnvironmentStore()
const freezeStore = useFreezeStore()

const selectedEnv = ref<Environment | null>(null)
const isEnvDialogOpen = ref(false)
const isPipelineDialogOpen = ref(false)
const envFormData = ref({ key: '', name: '' })
const pipelineFormData = ref({ key: '', name: '' })
const savingEnvironment = ref(false)
const savingPipeline = ref(false)
const envSaveError = ref('')
const pipelineSaveError = ref('')
const deleteTarget = ref<{ kind: 'environment' | 'pipeline'; key: string; name: string } | null>(null)
const deleteError = ref('')
const deleting = ref(false)
const channelCount = computed(() => environmentStore.environments.reduce((sum, env) => sum + env.pipelines.length, 0))

const displayPipelines = computed(() => selectedEnv.value?.pipelines || [])

// 选中环境后加载其全部渠道的冻结状态
watch(selectedEnv, (env) => {
  if (env) {
    freezeStore.loadMany(env.pipelines.map((pipeline) => ({ environmentKey: env.key, pipelineKey: pipeline.key })))
  }
}, { immediate: true })

const pipelineLocked = (environmentKey: string, pipelineKey: string) =>
  freezeStore.isLocked(environmentKey, pipelineKey)

const pipelineFreezeReason = (environmentKey: string, pipelineKey: string) =>
  freezeStore.statusFor(environmentKey, pipelineKey)?.last_reason || ''

const envHasLockedPipeline = (env: Environment) =>
  env.pipelines.some((pipeline) => pipelineLocked(env.key, pipeline.key))

onMounted(() => {
  environmentStore.fetchOverview()
})

watch(() => environmentStore.environments, (envs) => {
  if (selectedEnv.value) {
    const updated = envs.find((e) => e.key === selectedEnv.value?.key)
    if (updated) {
      selectedEnv.value = updated
    } else {
      selectedEnv.value = null
    }
  }
})

const handleAddEnv = async () => {
  if (savingEnvironment.value || !envFormData.value.key || !envFormData.value.name) return
  savingEnvironment.value = true
  envSaveError.value = ''
  try {
    await environmentStore.createEnvironment({ ...envFormData.value })
    envFormData.value = { key: '', name: '' }
    isEnvDialogOpen.value = false
  } catch (error) {
    envSaveError.value = error instanceof Error ? error.message : '保存失败，请重试'
  } finally {
    savingEnvironment.value = false
  }
}

const handleDeleteEnv = async (key: string) => {
  try {
    await environmentStore.deleteEnvironment(key)
    if (selectedEnv.value?.key === key) selectedEnv.value = null
    return true
  } catch {
    // 含冻结渠道的环境会被服务端拒绝;刷新状态让禁用原因可见(错误提示由请求层展示)
    if (selectedEnv.value) {
      await freezeStore.loadMany(selectedEnv.value.pipelines.map((pipeline) => ({ environmentKey: selectedEnv.value!.key, pipelineKey: pipeline.key })))
    }
    return false
  }
}

const handleAddPipeline = async () => {
  if (savingPipeline.value || !selectedEnv.value || !pipelineFormData.value.key || !pipelineFormData.value.name) return
  savingPipeline.value = true
  pipelineSaveError.value = ''
  try {
    await environmentStore.createPipeline(selectedEnv.value.key, { ...pipelineFormData.value })
    pipelineFormData.value = { key: '', name: '' }
    isPipelineDialogOpen.value = false
  } catch (error) {
    pipelineSaveError.value = error instanceof Error ? error.message : '保存失败，请重试'
  } finally {
    savingPipeline.value = false
  }
}

const handleDeletePipeline = async (pipelineKey: string) => {
  if (!selectedEnv.value) return
  try {
    await environmentStore.deletePipeline(selectedEnv.value.key, pipelineKey)
    return true
  } catch {
    await freezeStore.loadStatus(selectedEnv.value.key, pipelineKey)
    return false
  }
}

const requestDelete = (kind: 'environment' | 'pipeline', key: string, name: string) => {
  deleteTarget.value = { kind, key, name }
  deleteError.value = ''
}

const confirmDelete = async () => {
  const target = deleteTarget.value
  if (!target || deleting.value) return
  deleting.value = true
  deleteError.value = ''
  try {
    const ok = target.kind === 'environment' ? await handleDeleteEnv(target.key) : await handleDeletePipeline(target.key)
    if (ok) deleteTarget.value = null
    else deleteError.value = '删除未完成，请检查冻结状态后重试。'
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <SpectrumLayout>
    <div class="spectrum-operations spectrum-environments-page">
    <!-- §2.1 / §3.6: environment catalog → selected channels → scoped configuration.
         Scale and semantic states reuse the approved spectrum tokens. -->
    <SpectrumPageHeader eyebrow="ENVIRONMENTS / WORKSPACE MAP" title="环境管理" description="把环境与渠道分清，让每一份配置各有归属。">
      <span><strong>{{ environmentStore.environments.length }}</strong>环境</span>
      <span><strong>{{ channelCount }}</strong>渠道</span>
    </SpectrumPageHeader>
    <div class="spectrum-env-columns">
      <Card class="spectrum-panel spectrum-env-catalog">
        <CardHeader class="spectrum-panel-header">
          <div class="flex items-center justify-between">
            <CardTitle class="text-lg">环境列表</CardTitle>
            <Dialog v-model:open="isEnvDialogOpen">
              <DialogTrigger as-child>
                <Button size="sm">
                  <Plus class="w-4 h-4 mr-2" />
                  新增环境
                </Button>
              </DialogTrigger>
              <DialogContent>
                <DialogHeader>
                  <DialogTitle>新增环境</DialogTitle>
                  <DialogDescription>添加一个新的部署环境</DialogDescription>
                </DialogHeader>
                <div class="space-y-4">
                  <div class="space-y-2">
                    <Label for="env-key">环境标识</Label>
                    <Input
                      id="env-key"
                      placeholder="例如: dev, test, prod"
                      v-model="envFormData.key"
                    />
                  </div>
                  <div class="space-y-2">
                    <Label for="env-name">环境名称</Label>
                    <Input
                      id="env-name"
                      placeholder="例如: 开发环境"
                      v-model="envFormData.name"
                    />
                  </div>
                  <p v-if="envSaveError" class="text-sm text-destructive" role="alert">{{ envSaveError }}</p>
                  <DialogFooter>
                    <Button variant="outline" :disabled="savingEnvironment" @click="isEnvDialogOpen = false">
                      取消
                    </Button>
                    <Button
                      @click="handleAddEnv"
                      :disabled="savingEnvironment || !envFormData.key || !envFormData.name"
                    >
                      <Loader2 v-if="savingEnvironment" class="w-4 h-4 mr-2 animate-spin" />
                      保存
                    </Button>
                  </DialogFooter>
                </div>
              </DialogContent>
            </Dialog>
          </div>
        </CardHeader>
        <CardContent class="spectrum-panel-content">
          <div v-if="environmentStore.loading" class="flex items-center justify-center py-8">
            <Loader2 class="w-6 h-6 animate-spin text-muted-foreground" />
          </div>
          <div v-else-if="environmentStore.error" class="text-center py-8 text-destructive">
            <p>加载失败，请重试</p><Button variant="outline" class="mt-4" @click="environmentStore.fetchOverview()">重新加载</Button>
          </div>
          <div v-else class="space-y-2">
            <div
              v-for="env in environmentStore.environments"
              :key="env.key"
              class="spectrum-env-row"
              :class="{ 'is-selected': selectedEnv?.key === env.key }"
            >
              <button class="spectrum-env-select" :aria-pressed="selectedEnv?.key === env.key" @click="selectedEnv = env">
                <Layers :size="20" aria-hidden="true" />
                <span><strong>{{ env.name }}</strong><code>{{ env.key }}</code></span>
              </button>
              <div class="flex items-center gap-2">
                <Badge variant="secondary" class="text-xs">
                  {{ env.pipelines.length }} 渠道
                </Badge>
                <span v-if="envHasLockedPipeline(env)" class="spectrum-state-pill is-frozen">
                  <Lock class="w-3 h-3 mr-1" />
                  含冻结渠道
                </span>
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-8 w-8"
                  :disabled="environmentStore.loading || envHasLockedPipeline(env)"
                  :title="envHasLockedPipeline(env) ? '包含已冻结渠道的环境不可删除' : undefined"
                  :aria-label="`删除环境 ${env.name}`"
                  @click.stop="requestDelete('environment', env.key, env.name)"
                >
                  <Loader2 v-if="environmentStore.loading" class="w-4 h-4 animate-spin" />
                  <Trash2 v-else class="w-4 h-4 text-destructive" />
                </Button>
              </div>
            </div>
            <p v-if="environmentStore.environments.length === 0" class="text-center py-8 text-muted-foreground">
              暂无环境，请添加
            </p>
          </div>
        </CardContent>
      </Card>

      <Card class="spectrum-panel spectrum-env-channels">
        <CardHeader class="spectrum-panel-header">
          <div class="flex items-center justify-between">
            <CardTitle class="text-lg">
              {{ selectedEnv ? `${selectedEnv.name} - 渠道管理` : '渠道管理' }}
            </CardTitle>
            <Dialog v-if="selectedEnv" v-model:open="isPipelineDialogOpen">
              <DialogTrigger as-child>
                <Button size="sm" variant="outline">
                  <Plus class="w-4 h-4 mr-2" />
                  新增渠道
                </Button>
              </DialogTrigger>
              <DialogContent>
                <DialogHeader>
                  <DialogTitle>新增渠道</DialogTitle>
                  <DialogDescription>
                    为 {{ selectedEnv.name }} 添加渠道
                  </DialogDescription>
                </DialogHeader>
                <div class="space-y-4">
                  <div class="space-y-2">
                    <Label for="pipeline-key">渠道标识</Label>
                    <Input
                      id="pipeline-key"
                      placeholder="例如: default, gray"
                      v-model="pipelineFormData.key"
                    />
                  </div>
                  <div class="space-y-2">
                    <Label for="pipeline-name">渠道名称</Label>
                    <Input
                      id="pipeline-name"
                      placeholder="例如: 默认渠道"
                      v-model="pipelineFormData.name"
                    />
                  </div>
                  <p v-if="pipelineSaveError" class="text-sm text-destructive" role="alert">{{ pipelineSaveError }}</p>
                  <DialogFooter>
                    <Button variant="outline" :disabled="savingPipeline" @click="isPipelineDialogOpen = false">
                      取消
                    </Button>
                    <Button
                      @click="handleAddPipeline"
                      :disabled="savingPipeline || !pipelineFormData.key || !pipelineFormData.name"
                    >
                      <Loader2 v-if="savingPipeline" class="w-4 h-4 mr-2 animate-spin" />
                      保存
                    </Button>
                  </DialogFooter>
                </div>
              </DialogContent>
            </Dialog>
          </div>
        </CardHeader>
        <CardContent class="spectrum-panel-content">
          <div v-if="selectedEnv" class="spectrum-env-context">
            <p class="spectrum-eyebrow">SELECTED ENVIRONMENT</p>
            <h2>{{ selectedEnv.name }}</h2>
            <div><code>{{ selectedEnv.key }}</code><span>{{ displayPipelines.length }} 个渠道</span></div>
          </div>
          <div v-if="selectedEnv" class="space-y-2">
            <div
              v-for="pipeline in displayPipelines"
              :key="pipeline.key"
              class="spectrum-channel-row"
            >
              <div>
                <p class="font-medium text-sm">{{ pipeline.name }}</p>
                <p class="text-xs text-muted-foreground font-mono">
                  {{ pipeline.key }}
                </p>
              </div>
              <div class="flex items-center gap-2">
                <span v-if="pipelineLocked(selectedEnv.key, pipeline.key)" class="spectrum-state-pill is-frozen" :title="pipelineFreezeReason(selectedEnv.key, pipeline.key)">
                  <Lock class="w-3 h-3 mr-1" />
                  已冻结
                </span>
                <RouterLink class="spectrum-quiet-link" :to="{ path: '/config', query: { env: selectedEnv.key, pipeline: pipeline.key } }" :aria-label="`管理 ${pipeline.name} 配置`">配置 <ArrowUpRight :size="14" /></RouterLink>
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-8 w-8"
                  :disabled="environmentStore.loading || pipelineLocked(selectedEnv.key, pipeline.key)"
                  :title="pipelineLocked(selectedEnv.key, pipeline.key) ? `已冻结:${pipelineFreezeReason(selectedEnv.key, pipeline.key) || '该渠道不可删除'}` : undefined"
                  :aria-label="`删除渠道 ${pipeline.name}`"
                  @click="requestDelete('pipeline', pipeline.key, pipeline.name)"
                >
                  <Loader2 v-if="environmentStore.loading" class="w-4 h-4 animate-spin" />
                  <Trash2 v-else class="w-4 h-4 text-destructive" />
                </Button>
              </div>
            </div>
            <p v-if="displayPipelines.length === 0" class="text-center py-8 text-muted-foreground">
              暂无渠道，请添加
            </p>
          </div>
          <div v-else class="spectrum-empty"><GitBranch :size="32" /><h3>请先选择一个环境</h3><p>在左侧选择环境，查看渠道并进入对应的配置工作区。</p></div>
        </CardContent>
      </Card>
    </div>
    <Dialog :open="Boolean(deleteTarget)" @update:open="open => { if (!open && !deleting) deleteTarget = null }">
      <DialogContent><DialogHeader><DialogTitle>{{ deleteTarget?.kind === 'environment' ? '删除环境' : '删除渠道' }}</DialogTitle><DialogDescription>将删除 {{ deleteTarget?.name }}（{{ deleteTarget?.key }}）。此操作不可撤销，请确认目标范围。</DialogDescription></DialogHeader><p v-if="deleteError" role="alert" class="text-sm text-destructive">{{ deleteError }}</p><DialogFooter><Button variant="outline" :disabled="deleting" @click="deleteTarget = null">取消</Button><Button variant="destructive" :disabled="deleting" @click="confirmDelete">{{ deleting ? '删除中…' : '确认删除' }}</Button></DialogFooter></DialogContent>
    </Dialog>
    </div>
  </SpectrumLayout>
</template>
