<script setup lang="ts">
import { useAuthStore } from '@/stores/auth'
import { Button } from '@/components/ui/button'
const auth = useAuthStore()
</script>
<template>
  <main class="spectrum-shell spectrum-auth-loading">
    <span class="spectrum-mark" aria-hidden="true" />
    <h1>暂时无法确认登录状态</h1>
    <p role="alert">{{ auth.error }}</p>
    <Button :disabled="auth.restoring" @click="auth.restore(true)">{{ auth.restoring ? '确认中…' : '重试会话恢复' }}</Button>
    <RouterLink to="/login" class="spectrum-login-back">返回登录页</RouterLink>
  </main>
</template>
