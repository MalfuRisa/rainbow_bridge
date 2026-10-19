<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { Layout } from '@/components/ui/layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogTrigger, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Plus, Trash2, GitBranch, ChevronRight, Loader2, Lock } from 'lucide-vue-next'
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
  if (selectedEnv.value && envs.length > 0) {
    const updated = envs.find((e) => e.key === selectedEnv.value?.key)
    if (updated) {
      selectedEnv.value = updated
    } else {
      selectedEnv.value = null
    }
  }
})

const handleAddEnv = async () => {
  if (!envFormData.value.key || !envFormData.value.name) return
  await environmentStore.createEnvironment(envFormData.value)
  envFormData.value = { key: '', name: '' }
  isEnvDialogOpen.value = false
}

const handleDeleteEnv = async (key: string) => {
  try {
    await environmentStore.deleteEnvironment(key)
    if (selectedEnv.value?.key === key) selectedEnv.value = null
  } catch {
    // 含冻结渠道的环境会被服务端拒绝;刷新状态让禁用原因可见(错误提示由请求层展示)
    if (selectedEnv.value) {
      await freezeStore.loadMany(selectedEnv.value.pipelines.map((pipeline) => ({ environmentKey: selectedEnv.value!.key, pipelineKey: pipeline.key })))
    }
  }
}

const handleAddPipeline = async () => {
  if (!selectedEnv.value || !pipelineFormData.value.key || !pipelineFormData.value.name) return
  await environmentStore.createPipeline(selectedEnv.value.key, pipelineFormData.value)
  pipelineFormData.value = { key: '', name: '' }
  isPipelineDialogOpen.value = false
}

const handleDeletePipeline = async (pipelineKey: string) => {
  if (!selectedEnv.value) return
  try {
    await environmentStore.deletePipeline(selectedEnv.value.key, pipelineKey)
  } catch {
    await freezeStore.loadStatus(selectedEnv.value.key, pipelineKey)
  }
}
</script>

<template>
  <Layout
    title="环境管理"
    description="管理多环境配置和渠道"
  >
    <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
      <Card>
        <CardHeader>
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
                  <DialogFooter>
                    <Button variant="outline" @click="isEnvDialogOpen = false">
                      取消
                    </Button>
                    <Button
                      @click="handleAddEnv"
                      :disabled="environmentStore.loading"
                    >
                      <Loader2 v-if="environmentStore.loading" class="w-4 h-4 mr-2 animate-spin" />
                      保存
                    </Button>
                  </DialogFooter>
                </div>
              </DialogContent>
            </Dialog>
          </div>
        </CardHeader>
        <CardContent>
          <div v-if="environmentStore.loading" class="flex items-center justify-center py-8">
            <Loader2 class="w-6 h-6 animate-spin text-muted-foreground" />
          </div>
          <div v-else-if="environmentStore.error" class="text-center py-8 text-destructive">
            加载失败，请刷新重试
          </div>
          <div v-else class="space-y-2">
            <div
              v-for="env in environmentStore.environments"
              :key="env.key"
              :class="[
                'flex items-center justify-between p-3 rounded-lg border cursor-pointer transition-colors',
                selectedEnv?.key === env.key
                  ? 'border-primary bg-accent'
                  : 'hover:bg-accent/50'
              ]"
              @click="selectedEnv = env"
            >
              <div class="flex items-center gap-3">
                <div class="p-2 rounded-md bg-primary/10">
                  <GitBranch class="w-4 h-4 text-primary" />
                </div>
                <div>
                  <p class="font-medium text-sm">{{ env.name }}</p>
                  <p class="text-xs text-muted-foreground font-mono">
                    {{ env.key }}
                  </p>
                </div>
              </div>
              <div class="flex items-center gap-2">
                <Badge variant="secondary" class="text-xs">
                  {{ env.pipelines.length }} 渠道
                </Badge>
                <Badge v-if="envHasLockedPipeline(env)" variant="destructive" class="text-xs px-2 py-0.5">
                  <Lock class="w-3 h-3 mr-1" />
                  含冻结渠道
                </Badge>
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-8 w-8"
                  :disabled="environmentStore.loading || envHasLockedPipeline(env)"
                  :title="envHasLockedPipeline(env) ? '包含已冻结渠道的环境不可删除' : undefined"
                  @click.stop="handleDeleteEnv(env.key)"
                >
                  <Loader2 v-if="environmentStore.loading" class="w-4 h-4 animate-spin" />
                  <Trash2 v-else class="w-4 h-4 text-destructive" />
                </Button>
                <ChevronRight class="w-4 h-4 text-muted-foreground" />
              </div>
            </div>
            <p v-if="environmentStore.environments.length === 0" class="text-center py-8 text-muted-foreground">
              暂无环境，请添加
            </p>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div class="flex items-center justify-between">
            <CardTitle class="text-lg">
              {{ selectedEnv ? `${selectedEnv.name} - 渠道管理` : '渠道管理' }}
            </CardTitle>
            <Dialog v-if="selectedEnv" v-model:open="isPipelineDialogOpen">
              <DialogTrigger as-child>
                <Button size="sm">
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
                  <DialogFooter>
                    <Button variant="outline" @click="isPipelineDialogOpen = false">
                      取消
                    </Button>
                    <Button
                      @click="handleAddPipeline"
                      :disabled="environmentStore.loading"
                    >
                      <Loader2 v-if="environmentStore.loading" class="w-4 h-4 mr-2 animate-spin" />
                      保存
                    </Button>
                  </DialogFooter>
                </div>
              </DialogContent>
            </Dialog>
          </div>
        </CardHeader>
        <CardContent>
          <div v-if="selectedEnv" class="space-y-2">
            <div
              v-for="pipeline in displayPipelines"
              :key="pipeline.key"
              class="flex items-center justify-between p-3 rounded-lg border"
            >
              <div>
                <p class="font-medium text-sm">{{ pipeline.name }}</p>
                <p class="text-xs text-muted-foreground font-mono">
                  {{ pipeline.key }}
                </p>
              </div>
              <div class="flex items-center gap-2">
                <Badge v-if="pipelineLocked(selectedEnv.key, pipeline.key)" variant="destructive" class="text-xs px-2 py-0.5" :title="pipelineFreezeReason(selectedEnv.key, pipeline.key)">
                  <Lock class="w-3 h-3 mr-1" />
                  已冻结
                </Badge>
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-8 w-8"
                  :disabled="environmentStore.loading || pipelineLocked(selectedEnv.key, pipeline.key)"
                  :title="pipelineLocked(selectedEnv.key, pipeline.key) ? `已冻结:${pipelineFreezeReason(selectedEnv.key, pipeline.key) || '该渠道不可删除'}` : undefined"
                  @click="handleDeletePipeline(pipeline.key)"
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
          <p v-else class="text-center py-8 text-muted-foreground">
            请先选择一个环境
          </p>
        </CardContent>
      </Card>
    </div>
  </Layout>
</template>
