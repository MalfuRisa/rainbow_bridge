<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
const auth = useAuthStore()
const router = useRouter()
async function logout() {
  if (await auth.logout()) await router.replace('/login')
}
</script>
<template>
  <div class="spectrum-account">
    <template v-if="auth.authenticated"><span class="spectrum-account-name" :title="auth.user?.username">{{ auth.user?.username }}</span><button :disabled="auth.submitting" @click="logout">{{ auth.submitting ? '退出中…' : '退出' }}</button></template>
    <RouterLink v-else-if="auth.status !== 'unknown'" to="/login">登录</RouterLink>
    <span v-else role="status">确认身份…</span>
    <p v-if="auth.error && auth.authenticated" class="spectrum-account-error" role="alert">{{ auth.error }}<button :disabled="auth.submitting" @click="logout">重试退出</button></p>
  </div>
</template>
