<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowUpRight, ChevronDown, Menu, X } from 'lucide-vue-next'
import { useVersionStore } from '@/stores/version'
import '@/styles/spectrum.css'
import WorkspaceAccount from '@/components/auth/WorkspaceAccount.vue'
import SessionNotice from '@/components/auth/SessionNotice.vue'
import ScopeNotice from './ScopeNotice.vue'
import { getBasePath } from '@/lib/utils'
import TopologyBackground from './TopologyBackground.vue'

// Shared workspace shell; semantic tokens also reach Radix portals.
withDefaults(defineProps<{ elevated?: boolean }>(), { elevated: true })
const route = useRoute()
const brandIconUrl = `${getBasePath().replace(/\/$/, '')}/icon.svg`
const versionStore = useVersionStore()
const menuOpen = ref(false)
const navigation = [
  { to: '/', label: '项目概览' },
  { to: '/config', label: '配置管理' },
  { to: '/environments', label: '环境管理' },
  { to: '/resources', label: '资源管理' },
  { to: '/migration', label: '配置迁移' },
  { to: '/import-export', label: '导入导出' },
  { to: '/releases', label: '版本发布' },
  { to: '/backups', label: '备份恢复' },
]
const version = computed(() => versionStore.versionInfo?.version || '')
const hasNewVersion = computed(() => Boolean(version.value && versionStore.latestRelease && version.value !== versionStore.latestRelease.tag_name && !versionStore.latestRelease.prerelease))
watch(() => route.path, () => { menuOpen.value = false })
onMounted(() => { void versionStore.init() })
</script>

<template>
  <div class="spectrum-shell" :class="{ 'spectrum-shell--elevated': elevated }">
    <TopologyBackground />
    <a class="spectrum-skip" href="#spectrum-main">跳转到主要内容</a>
    <header class="spectrum-header">
      <RouterLink to="/" class="spectrum-brand" aria-label="虹桥计划首页">
        <span class="spectrum-mark" aria-hidden="true"><img :src="brandIconUrl" alt="" width="48" height="48" /></span>
        <span><strong>虹桥计划</strong><small>RAINBOW BRIDGE</small></span>
      </RouterLink>
      <button class="spectrum-menu-button" :aria-expanded="menuOpen" aria-controls="spectrum-navigation" :aria-label="menuOpen ? '收起导航' : '展开导航'" @click="menuOpen = !menuOpen">
        <X v-if="menuOpen" :size="20" /><Menu v-else :size="20" />
        <span>导航</span>
      </button>
      <nav id="spectrum-navigation" class="spectrum-navigation" :class="{ 'is-open': menuOpen }" aria-label="主要导航">
        <RouterLink v-for="item in navigation" :key="item.to" :to="item.to" :aria-current="route.path === item.to ? 'page' : undefined">{{ item.label }}</RouterLink>
      </nav>
      <a class="spectrum-doc-link" href="https://yi-nology.github.io/rainbow_bridge/guide/" target="_blank" rel="noopener noreferrer">使用文档 <ArrowUpRight :size="14" /></a>
      <WorkspaceAccount />
    </header>
    <main id="spectrum-main" class="spectrum-main" tabindex="-1"><SessionNotice /><ScopeNotice /><slot /></main>
    <footer class="spectrum-footer">
      <details v-if="versionStore.versionInfo" class="spectrum-build-info">
        <summary>RAINBOW BRIDGE <code>{{ version }}</code><ChevronDown :size="12" aria-hidden="true" /></summary>
        <dl><dt>当前版本</dt><dd>{{ version }}</dd><dt>Commit</dt><dd>{{ versionStore.versionInfo.git_commit || '—' }}</dd><dt>Build</dt><dd>{{ versionStore.versionInfo.build_time || '—' }}</dd></dl>
        <a v-if="versionStore.latestRelease" :href="versionStore.latestRelease.html_url" target="_blank" rel="noopener noreferrer">{{ hasNewVersion ? '有新版本' : '最新版本' }} {{ versionStore.latestRelease.tag_name }} · {{ versionStore.latestRelease.name }} <ArrowUpRight :size="14" /></a>
      </details>
      <span v-else>RAINBOW BRIDGE</span>
      <span>配置有序，发布有据。</span>
      <div class="spectrum-footer-links"><a href="https://yi-nology.github.io/rainbow_bridge/guide/" target="_blank" rel="noopener noreferrer">使用文档 <ArrowUpRight :size="14" /></a><a href="https://github.com/yi-nology/rainbow_bridge" target="_blank" rel="noopener noreferrer">GitHub <ArrowUpRight :size="14" /></a></div>
    </footer>
  </div>
</template>
