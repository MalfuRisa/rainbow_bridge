<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, ArrowRight, Loader2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useAuthStore } from '@/stores/auth'
import { ApiError } from '@/lib/api/error'
import { safeReturnTo } from '@/lib/auth/redirect'
import '@/styles/spectrum.css'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()
const username = ref('admin')
const password = ref('')
const error = ref('')
const coolingDown = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
onUnmounted(() => { clearTimeout(timer); password.value = '' })
async function enter() {
  if (auth.submitting || coolingDown.value || !username.value.trim() || !password.value) return
  error.value = ''
  try {
    if (await auth.login(username.value.trim(), password.value)) await router.replace(safeReturnTo(route.query.redirect))
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '服务暂不可用，请重试'
    if (cause instanceof ApiError && cause.code === 429) {
      const seconds = cause.retryAfter || 5
      error.value = `请求过于频繁，请 ${seconds} 秒后重试`
      coolingDown.value = true
      timer = setTimeout(() => { coolingDown.value = false }, seconds * 1000)
    }
  } finally { password.value = '' }
}
async function restore() {
  await auth.restore(true)
  if (auth.authenticated) await router.replace(safeReturnTo(route.query.redirect))
}
</script>

<template>
  <!-- Primary: account/password and submit. Secondary: return and recovery.
       Approved spectrum tokens; 48px controls, 24/32/48px group spacing. -->
  <div class="spectrum-shell spectrum-login-shell">
    <header class="spectrum-login-header">
      <RouterLink to="/" class="spectrum-brand" aria-label="虹桥计划首页">
        <span class="spectrum-mark" aria-hidden="true" />
        <span><strong>虹桥计划</strong><small>RAINBOW BRIDGE</small></span>
      </RouterLink>
      <RouterLink to="/" class="spectrum-login-back"><ArrowLeft :size="16" />返回项目概览</RouterLink>
    </header>
    <main class="spectrum-login-layout">
      <section class="spectrum-login-statement" aria-label="光谱工作台">
        <p class="spectrum-eyebrow">RAINBOW BRIDGE / 光谱工作台</p>
        <div><span class="spectrum-login-line" aria-hidden="true" /><p class="spectrum-login-motto">配置有序，<br>发布有据。</p></div>
        <p class="spectrum-login-caption">从配置到生效，<br>每一步都清晰可见。</p>
      </section>
      <section class="spectrum-login-form">
        <p class="spectrum-eyebrow">WORKSPACE ACCESS / 管理入口</p>
        <h1>登录工作台</h1>
        <p class="spectrum-login-description">登录后，继续管理环境、配置与发布。</p>
        <div v-if="auth.status === 'failed'" class="spectrum-auth-notice" role="alert">
          <p>暂时无法确认登录状态。{{ auth.error }}</p>
          <Button variant="outline" :disabled="auth.restoring" @click="restore">{{ auth.restoring ? '确认中…' : '重试会话恢复' }}</Button>
        </div>
        <form @submit.prevent="enter">
          <div class="spectrum-login-field"><Label for="login-username">账号</Label><Input id="login-username" v-model="username" autocomplete="username" required :disabled="auth.submitting" /></div>
          <div class="spectrum-login-field"><Label for="login-password">密码</Label><Input id="login-password" v-model="password" type="password" autocomplete="current-password" required :disabled="auth.submitting" /></div>
          <p v-if="error || (auth.error && auth.status !== 'failed')" class="spectrum-login-error" role="alert" aria-live="polite">{{ error || auth.error }}</p>
          <Button type="submit" class="spectrum-login-submit" :disabled="auth.submitting || coolingDown || !username.trim() || !password">
            <Loader2 v-if="auth.submitting" :size="18" class="animate-spin" />
            {{ auth.submitting ? '登录中…' : coolingDown ? '请稍后重试' : '登录' }}<ArrowRight v-if="!auth.submitting" :size="18" />
          </Button>
        </form>
      </section>
    </main>
    <footer class="spectrum-login-footer">RAINBOW BRIDGE<span>让内容有序，让发布清晰。</span></footer>
  </div>
</template>
