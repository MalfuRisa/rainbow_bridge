<script setup lang="ts">
import { ref, watch } from 'vue'
import { Check, ChevronDown, Copy } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import ConfigContentPreview from '@/components/Config/ConfigContentPreview.vue'
import SpectrumImagePreview from '@/components/spectrum/SpectrumImagePreview.vue'
import type { ReleaseComparison } from '@/lib/api/release-compare'
import type { ReleaseDetailConfig } from '@/lib/api/release'
import { toast } from 'vue-sonner'

// Primary: which configuration changed. Secondary: changed fields and two fixed
// version previews. Tertiary: raw/copy. Existing Spectrum surface/color tokens;
// 8/12/16/24 spacing, 12/14px text, 8/12px radius; no new palette or animation.
const props = defineProps<{
  result: ReleaseComparison
  before: Map<string, ReleaseDetailConfig>
  after: Map<string, ReleaseDetailConfig>
  loading: boolean
  error: string
}>()
const emit = defineEmits<{ load: [] }>()
const expanded = ref(new Set<string>())
const copied = ref('')
const image = ref<{ url: string; name: string } | null>(null)
const fields: Record<string, string> = { alias: '别名', name: '名称', type: '类型', content: '内容', remark: '备注', is_perm: '可见性', description: '描述' }
const changes = { added: '新增', modified: '修改', removed: '删除' }
watch(() => props.result.target.snapshot_id, () => { expanded.value.clear(); image.value = null; copied.value = '' })
function toggle(key: string) {
  if (expanded.value.has(key)) expanded.value.delete(key)
  else { expanded.value.add(key); emit('load') }
}
async function copy(config: ReleaseDetailConfig, side: string) {
  try { await navigator.clipboard.writeText(config.content); copied.value = side + config.resource_key }
  catch { toast.error('复制失败，请选择原文后手动复制') }
}
</script>

<template>
  <div class="comparison-list">
    <article v-for="item in result.items" :key="item.resource_key" class="comparison-item">
      <button class="comparison-row" :aria-expanded="expanded.has(item.resource_key)" :aria-controls="`change-${item.resource_key}`" @click="toggle(item.resource_key)">
        <span class="comparison-kind" :class="`is-${item.change_type}`">{{ changes[item.change_type] }}</span>
        <span class="comparison-name"><strong>{{ item.after?.name || item.before?.name || item.resource_key }}</strong><code>{{ item.after?.alias || item.before?.alias }}</code></span>
        <span class="comparison-fields">{{ item.changed_fields.map(key => fields[key] || key).join('、') }}</span>
        <ChevronDown :size="16" :class="{ 'rotate-180': expanded.has(item.resource_key) }" />
      </button>
      <div v-if="expanded.has(item.resource_key)" :id="`change-${item.resource_key}`" class="comparison-expanded">
        <p v-if="loading" role="status" class="text-sm text-muted-foreground">正在读取版本内容…</p>
        <div v-else-if="error" role="alert" class="text-sm text-destructive">{{ error }} <Button variant="outline" size="sm" @click="emit('load')">重试内容读取</Button></div>
        <div v-else class="comparison-columns">
          <section v-for="side in (['before', 'after'] as const)" :key="side" class="comparison-preview">
            <header><span>{{ side === 'before' ? `${result.comparison_mode === 'fixed' ? '基准' : '当前生效'} · ${result.baseline?.version ?? '无'}` : `${result.comparison_mode === 'fixed' ? '目标' : '候选'} · ${result.target.version}` }}</span>
              <Button v-if="(side === 'before' ? before : after).has(item.resource_key)" size="sm" variant="ghost" :aria-label="`复制${result.comparison_mode === 'fixed' ? (side === 'before' ? '基准版本' : '目标版本') : (side === 'before' ? '生效版本' : '候选版本')} ${item.after?.alias || item.before?.alias} 原文`" @click="copy((side === 'before' ? before : after).get(item.resource_key)!, side)">
                <Check v-if="copied === side + item.resource_key" :size="14" /><Copy v-else :size="14" />{{ copied === side + item.resource_key ? '已复制' : '复制' }}
              </Button>
            </header>
            <template v-if="(side === 'before' ? before : after).get(item.resource_key)">
              <ConfigContentPreview :config="(side === 'before' ? before : after).get(item.resource_key)!" @image-preview="(url, name) => image = { url, name }" />
              <dl class="comparison-metadata">
                <template v-for="field in item.changed_fields.filter(key => key !== 'content')" :key="field">
                  <dt>{{ fields[field] || field }}</dt><dd>{{ (side === 'before' ? before : after).get(item.resource_key)?.[field as keyof ReleaseDetailConfig] ?? '—' }}</dd>
                </template>
              </dl>
            </template>
            <p v-else class="text-sm text-muted-foreground">{{ side === 'before' ? '此版本没有该配置' : '此配置将被移除' }}</p>
          </section>
        </div>
      </div>
    </article>
    <SpectrumImagePreview :url="image?.url ?? null" :name="image?.name ?? ''" @close="image = null" />
  </div>
</template>

<style scoped>
.comparison-list { display: grid; gap: 8px; }
.comparison-item { border: 1px solid var(--spectrum-surface-line); border-radius: 12px; background: var(--card); overflow: hidden; }
.comparison-row { width: 100%; display: flex; align-items: center; text-align: left; gap: 16px; padding: 16px; cursor: pointer; }
.comparison-row:hover { background: var(--spectrum-hover); }
.comparison-row:focus-visible { outline: 2px solid var(--ring); outline-offset: -2px; }
.comparison-name { display: grid; gap: 4px; min-width: 0; flex: 1; overflow-wrap: anywhere; }
.comparison-name strong { font-size: 14px; font-weight: 600; }
.comparison-name code, .comparison-fields { color: var(--muted-foreground); font-size: 12px; }
.comparison-fields { max-width: 40%; }
.comparison-kind { padding: 4px 8px; border-radius: 8px; color: var(--spectrum-link); background: var(--spectrum-material-context); font-size: 12px; flex-shrink: 0; }
.comparison-kind.is-removed { color: var(--destructive); background: color-mix(in srgb, var(--destructive) 8%, var(--card)); }
.comparison-expanded { padding: 0 16px 16px; }
.comparison-columns { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.comparison-preview { min-width: 0; padding: 16px; border-radius: 8px; background: var(--spectrum-material-soft); }
.comparison-preview header { display: flex; justify-content: space-between; align-items: center; gap: 8px; margin-bottom: 16px; font-size: 12px; color: var(--muted-foreground); }
.comparison-preview header button { display: flex; gap: 8px; }
.comparison-metadata { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 8px 16px; font-size: 12px; margin-top: 12px; overflow-wrap: anywhere; }
.comparison-metadata dt { color: var(--muted-foreground); }
</style>
