<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { LayoutDashboard, Settings, Github, Database, Layers, ArrowRightLeft, Info, ExternalLink, Import, BookOpen, PackageCheck } from 'lucide-vue-next'
import { cn } from '@/lib/utils'
import { useVersionStore } from '@/stores/version'
import { Badge } from '@/components/ui/badge'

const route = useRoute()
const versionStore = useVersionStore()

const navItems = [
  {
    title: '项目概览',
    href: '/',
    icon: LayoutDashboard,
  },
  {
    title: '配置管理',
    href: '/config',
    icon: Settings,
  },
  {
    title: '环境管理',
    href: '/environments',
    icon: Layers,
  },
  {
    title: '资源管理',
    href: '/resources',
    icon: Database,
  },
  {
    title: '配置迁移',
    href: '/migration',
    icon: ArrowRightLeft,
  },
  {
    title: '导入导出',
    href: '/import-export',
    icon: Import,
  },
  {
    title: '版本发布',
    href: '/releases',
    icon: PackageCheck,
  },
]

const isActive = (href: string) => {
  return route.path === href
}

const hasNewVersion = computed(() => {
  if (!versionStore.versionInfo?.version || !versionStore.latestRelease?.tag_name) return false
  return versionStore.versionInfo.version !== versionStore.latestRelease.tag_name && !versionStore.latestRelease.prerelease
})

import { getBasePath } from '@/lib/utils'

const basePath = getBasePath()
const normalizedBasePath = basePath.endsWith('/') ? basePath : `${basePath}/`

onMounted(() => {
  versionStore.init()
})
</script>

<template>
  <aside class="w-64 min-h-screen border-r border-border bg-sidebar flex flex-col sm:w-72">
    <div class="p-4 sm:p-6 border-b border-sidebar-border">
      <router-link to="/" class="flex items-center gap-3">
        <img :src="`${normalizedBasePath}icon.svg`" alt="Rainbow Bridge Logo" class="w-8 h-8 sm:w-10 sm:h-10" />
        <div class="hidden sm:block">
          <h1 class="font-semibold text-sidebar-foreground">虹桥计划</h1>
          <p class="text-xs text-muted-foreground">Rainbow Bridge</p>
        </div>
      </router-link>
    </div>

    <nav class="flex-1 p-3 sm:p-4">
      <ul class="space-y-1">
        <li v-for="item in navItems" :key="item.href">
          <router-link
            :to="item.href"
            :class="cn(
              'flex items-center gap-2 sm:gap-3 px-2 sm:px-3 py-2 rounded-lg text-sm transition-colors',
              isActive(item.href)
                ? 'bg-sidebar-accent text-sidebar-accent-foreground font-medium'
                : 'text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground'
            )"
          >
            <component :is="item.icon" class="w-4 h-4" />
            <span class="hidden sm:inline">{{ item.title }}</span>
          </router-link>
        </li>
      </ul>
    </nav>

    <div class="p-3 sm:p-4 border-t border-sidebar-border space-y-3">
      <div v-if="versionStore.versionInfo" class="px-2 sm:px-3 py-2 rounded-lg bg-sidebar-accent/50">
        <div class="flex items-center justify-between mb-1">
          <div class="flex items-center gap-2">
            <Info class="w-3.5 h-3.5 text-muted-foreground" />
            <span class="text-xs font-medium text-muted-foreground hidden sm:inline">当前版本</span>
          </div>
          <Badge variant="secondary" class="text-xs font-mono">
            {{ versionStore.versionInfo.version }}
          </Badge>
        </div>
        <div class="text-[10px] text-muted-foreground space-y-0.5 hidden sm:block">
          <div class="truncate" :title="versionStore.versionInfo.git_commit">
            Commit: {{ versionStore.versionInfo.git_commit?.substring(0, 7) }}
          </div>
          <div class="truncate" :title="versionStore.versionInfo.build_time">
            Build: {{ versionStore.versionInfo.build_time ? new Date(versionStore.versionInfo.build_time).toLocaleDateString('zh-CN') : '-' }}
          </div>
        </div>
      </div>

      <a
        v-if="versionStore.latestRelease"
        :href="versionStore.latestRelease.html_url"
        target="_blank"
        rel="noopener noreferrer"
        class="block px-2 sm:px-3 py-2 rounded-lg hover:bg-sidebar-accent transition-colors"
      >
        <div class="flex items-center justify-between mb-1">
          <div class="flex items-center gap-2">
            <ExternalLink class="w-3.5 h-3.5 text-muted-foreground" />
            <span class="text-xs font-medium text-muted-foreground hidden sm:inline">最新版本</span>
          </div>
          <div class="flex items-center gap-1">
            <Badge 
              :variant="hasNewVersion ? 'default' : 'secondary'" 
              class="text-xs font-mono"
            >
              {{ versionStore.latestRelease.tag_name }}
            </Badge>
            <span v-if="hasNewVersion" class="text-xs text-green-600 dark:text-green-400">• New</span>
          </div>
        </div>
        <div class="text-[10px] text-muted-foreground truncate hidden sm:block">
          {{ versionStore.latestRelease.name || '点击查看更新说明' }}
        </div>
      </a>
    </div>

    <div class="p-3 sm:p-4 border-t border-sidebar-border space-y-1">
      <a
        href="https://yi-nology.github.io/rainbow_bridge/guide/"
        target="_blank"
        rel="noopener noreferrer"
        class="flex items-center gap-2 sm:gap-3 px-2 sm:px-3 py-2 rounded-lg text-sm text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors"
      >
        <BookOpen class="w-4 h-4" />
        <span class="hidden sm:inline">使用文档</span>
        <ExternalLink class="w-3 h-3 ml-auto opacity-50" />
      </a>
      <a
        href="https://github.com/yi-nology/rainbow_bridge"
        target="_blank"
        rel="noopener noreferrer"
        class="flex items-center gap-2 sm:gap-3 px-2 sm:px-3 py-2 rounded-lg text-sm text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors"
      >
        <Github class="w-4 h-4" />
        <span class="hidden sm:inline">GitHub</span>
        <ExternalLink class="w-3 h-3 ml-auto opacity-50" />
      </a>
    </div>
  </aside>
</template>
