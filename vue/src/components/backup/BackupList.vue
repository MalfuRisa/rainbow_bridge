<script setup lang="ts">
import { Download, Trash2, Archive, Plus, Loader2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import type { BackupItem } from '@/lib/api/backup'
import { backupSize, backupTime } from './presentation'
defineProps<{ items: BackupItem[]; loading: boolean; canOperate: boolean; downloading: string; hasMore: boolean; supported: boolean }>()
defineEmits<{ create: []; download: [item: BackupItem]; remove: [item: BackupItem]; more: [] }>()
</script>
<template>
  <section class="spectrum-panel backup-list" aria-labelledby="backup-list-title">
    <div class="backup-section-heading"><div><p class="spectrum-eyebrow">BUSINESS BACKUP</p><h2 id="backup-list-title">业务备份</h2></div><Button :disabled="!canOperate" @click="$emit('create')"><Plus :size="16" />创建备份</Button></div>
    <p class="backup-secondary">保存整个实例的配置、资源、版本及历史记录。复制期间会短暂暂停业务写入。</p>
    <div v-if="loading" class="backup-empty" role="status"><Loader2 :size="24" class="animate-spin" />正在读取备份</div>
    <div v-else-if="!items.length" class="backup-empty"><Archive :size="32" /><h3>{{ supported ? '尚无业务备份' : '当前部署不可备份' }}</h3><p>{{ supported ? '创建一份备份，为业务数据保留恢复点。' : '满足部署条件后可创建整实例业务备份。' }}</p></div>
    <div v-else class="backup-table-wrap"><table class="backup-table"><thead><tr><th>备份时间与备注</th><th>内容</th><th>大小</th><th>操作</th></tr></thead><tbody>
      <tr v-for="item in items" :key="item.backup_id"><td><strong>{{ backupTime(item.consistent_at) }}</strong><p>{{ item.reason || '未填写备注' }}</p><small>{{ item.actor }}</small></td>
        <td><span>{{ item.counts.configs }} 配置 · {{ item.counts.assets }} 资源</span><small>{{ item.counts.environments }} 环境 · {{ item.counts.pipelines }} 渠道 · {{ item.counts.versions }} 版本</small></td><td>{{ backupSize(item.size) }}</td>
        <td><div class="backup-actions"><Button variant="ghost" :disabled="!!downloading || !supported" :aria-label="`下载备份 ${item.backup_id}`" @click="$emit('download', item)"><Loader2 v-if="downloading === item.backup_id" :size="16" class="animate-spin" /><Download v-else :size="16" />下载</Button><Button variant="ghost" class="backup-danger-text" :disabled="!canOperate || !!downloading" :aria-label="`删除备份 ${item.backup_id}`" @click="$emit('remove', item)"><Trash2 :size="16" /></Button></div></td>
      </tr>
    </tbody></table></div>
    <Button v-if="hasMore" variant="outline" :disabled="loading" @click="$emit('more')">加载更多备份</Button>
  </section>
</template>
