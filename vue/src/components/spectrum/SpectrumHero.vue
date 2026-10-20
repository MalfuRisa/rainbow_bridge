<script setup lang="ts">
import { ArrowRight, ArrowUpRight, LockKeyhole } from 'lucide-vue-next'
import type { ActiveReleaseView } from '@/composables/useActiveRelease'
defineProps<{ activeView: ActiveReleaseView | null }>()
</script>

<template>
  <!-- Hierarchy (§1.1 / §2.1): value + enter workspace first; release metaphor second.
       Same grayscale composition as the approved concept; semantic candidate tokens. -->
  <section class="spectrum-hero" aria-label="虹桥计划简介">
    <div class="spectrum-hero-copy">
      <p class="spectrum-eyebrow"><span class="spectrum-dot" /> SELF-HOSTED / 自托管配置</p>
      <h2>每一次配置，<br>都有清晰的<span>去向。</span></h2>
      <p class="spectrum-hero-description">从多环境编辑，到冻结、发布与版本切换。<br>让应用读取哪一份内容，始终清楚。</p>
      <div class="spectrum-hero-actions">
        <RouterLink :to="{ path: '/config', query: activeView ? { env: activeView.environmentKey, pipeline: activeView.pipelineKey } : {} }" class="spectrum-primary-link">进入配置工作区 <ArrowUpRight :size="18" /></RouterLink>
        <RouterLink to="/releases" class="spectrum-hero-link">查看发布版本 <ArrowRight :size="16" /></RouterLink>
      </div>
      <p class="spectrum-hero-foot">你的部署 · 你的环境 · 你的配置</p>
    </div>
    <div class="spectrum-release-art" role="img" :aria-label="activeView ? `当前选中范围的生效版本为 ${activeView.version}，编辑区修改不会立即生效` : '编辑内容经冻结创建快照，再显式切换为业务生效版本'">
      <div class="spectrum-art-heading"><span>从编辑，到生效</span><span>RELEASE / BRIDGE</span></div>
      <div class="spectrum-arch spectrum-arch-outer" /><div class="spectrum-arch spectrum-arch-inner" />
      <div class="spectrum-origin">
        <span>编辑区</span><div><code>{ }</code><b>一套配置</b></div>
        <small>{{ activeView ? `${activeView.environmentKey} / ${activeView.pipelineKey}` : '环境 / 渠道' }}</small>
        <div class="spectrum-sheet-lines"><i /><i /><i /></div>
      </div>
      <div class="spectrum-art-lock"><LockKeyhole :size="14" /> 冻结 · 创建版本</div>
      <div class="spectrum-snapshot">
        <span class="spectrum-snapshot-label"><i />{{ activeView ? '当前生效' : '发布快照' }}</span>
        <strong :title="activeView?.version">{{ activeView?.version || '快照' }}</strong>
        <small>IMMUTABLE SNAPSHOT</small>
        <span class="spectrum-snapshot-footer">{{ activeView ? '业务读取此版本' : '显式切换后生效' }} <ArrowUpRight :size="14" /></span>
      </div>
      <div class="spectrum-art-baseline"><span>编辑区修改</span><span>≠</span><span>立即生效</span></div>
    </div>
  </section>
  <div class="spectrum-principles">
    <div><span>01 /</span><h3>按范围，管理内容</h3><p>环境与渠道分清，各处配置各有归属。</p></div>
    <div><span>02 /</span><h3>用快照，保留版本</h3><p>从冻结内容创建版本，已发布内容不可改写。</p></div>
    <div><span>03 /</span><h3>由你，决定何时生效</h3><p>显式切换，或切回一个已发布版本。</p></div>
  </div>
</template>
