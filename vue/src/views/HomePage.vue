<script setup lang="ts">
import { shallowRef } from 'vue'
import { useStorage } from '@vueuse/core'
import { ChevronDown, ChevronUp, Code2, Info } from 'lucide-vue-next'
import SpectrumLayout from '@/components/spectrum/SpectrumLayout.vue'
import SpectrumHero from '@/components/spectrum/SpectrumHero.vue'
import type { ActiveReleaseView } from '@/composables/useActiveRelease'
import ProjectIntro from '@/components/ProjectIntro.vue'
import ApiDocs from '@/components/ApiDocs.vue'
import RuntimeConfig from '@/components/RuntimeConfig.vue'

const showIntroduction = useStorage(`rainbow-bridge:${import.meta.env.BASE_URL}:home-introduction`, true)
const activeView = shallowRef<ActiveReleaseView | null>(null)
</script>

<template>
  <SpectrumLayout>
    <div class="spectrum-home-heading">
      <div><h1>项目概览</h1><p>让内容有序，让发布清晰。</p></div>
      <button class="spectrum-text-button" :aria-expanded="showIntroduction" aria-controls="spectrum-introduction" @click="showIntroduction = !showIntroduction">
        {{ showIntroduction ? '收起介绍' : '展开产品介绍' }}
        <ChevronUp v-if="showIntroduction" :size="16" /><ChevronDown v-else :size="16" />
      </button>
    </div>
    <div id="spectrum-introduction" v-show="showIntroduction"><SpectrumHero :active-view="activeView" /></div>
    <RuntimeConfig @view-change="activeView = $event" />
    <div class="spectrum-home-docs">
      <details class="spectrum-disclosure">
        <summary><span><Code2 :size="18" />前端对接与 API 说明</span><ChevronDown :size="18" /></summary>
        <ApiDocs />
      </details>
      <details class="spectrum-disclosure">
        <summary><span><Info :size="18" />关于虹桥计划</span><ChevronDown :size="18" /></summary>
        <ProjectIntro />
      </details>
    </div>
  </SpectrumLayout>
</template>
