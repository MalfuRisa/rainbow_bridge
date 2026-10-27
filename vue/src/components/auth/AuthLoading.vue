<script setup lang="ts">
import { Loader2 } from 'lucide-vue-next'
import '@/styles/spectrum.css'
</script>
<template>
  <div class="spectrum-shell spectrum-auth-loading" role="status" aria-live="polite">
    <span class="spectrum-mark" aria-hidden="true" />
    <p><Loader2 :size="18" class="animate-spin" />正在确认登录状态…</p>
  </div>
</template>
