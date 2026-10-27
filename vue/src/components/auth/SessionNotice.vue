<script setup lang="ts">
import { sessionProblem } from '@/lib/auth/session'
import { useAuthStore } from '@/stores/auth'
import { Button } from '@/components/ui/button'
const auth = useAuthStore()
</script>
<template>
  <div v-if="sessionProblem" class="spectrum-auth-notice" role="alert">
    <p>{{ sessionProblem.message }}</p>
    <Button v-if="sessionProblem.refreshable" variant="outline" :disabled="auth.restoring" @click="auth.restore(true)">{{ auth.restoring ? '刷新中…' : '刷新登录状态' }}</Button>
  </div>
</template>
