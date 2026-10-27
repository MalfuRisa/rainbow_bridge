import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { authApi } from '@/lib/api/auth'
import { ApiError } from '@/lib/api/error'
import { isSessionError } from '@/lib/api/http'
import { acceptSession, clearSession, sessionGeneration, sessionIdentity, sessionStatus } from '@/lib/auth/session'

export const useAuthStore = defineStore('auth', () => {
  const restoring = ref(false)
  const submitting = ref(false)
  const error = ref('')
  let attempt = 0
  let inFlight: Promise<void> | null = null
  const authenticated = computed(() => sessionStatus.value === 'authenticated')
  const user = computed(() => sessionIdentity.value?.user ?? null)

  function restore(force = false): Promise<void> {
    if (submitting.value) return Promise.resolve()
    if (inFlight) return inFlight
    if (!force && sessionStatus.value !== 'unknown') return Promise.resolve()
    const current = ++attempt
    const batch = sessionGeneration()
    restoring.value = true
    error.value = ''
    inFlight = (async () => {
      try {
        const data = await authApi.me()
        if (current === attempt && batch === sessionGeneration()) acceptSession(data)
      } catch (cause) {
        if (current !== attempt || batch !== sessionGeneration()) return
        if (isSessionError(cause)) {
          clearSession()
          if (cause instanceof ApiError && cause.errorType === 'AUTH_FORBIDDEN') error.value = cause.message
        } else {
          clearSession('failed')
          error.value = cause instanceof Error ? cause.message : '服务暂不可用，请重试'
        }
      } finally {
        if (current === attempt) { restoring.value = false; inFlight = null }
      }
    })()
    return inFlight
  }

  async function login(username: string, password: string) {
    if (submitting.value) return false
    const current = ++attempt
    inFlight = null
    restoring.value = false
    clearSession('unknown', 'login')
    const batch = sessionGeneration()
    submitting.value = true
    error.value = ''
    try {
      const data = await authApi.login(username, password)
      if (current !== attempt || batch !== sessionGeneration()) return false
      acceptSession(data)
      return true
    } catch (cause) {
      if (current === attempt && batch === sessionGeneration()) clearSession()
      throw cause
    } finally {
      if (current === attempt) submitting.value = false
    }
  }

  async function logout() {
    if (submitting.value) return false
    const current = ++attempt
    const batch = sessionGeneration()
    inFlight = null
    restoring.value = false
    submitting.value = true
    error.value = ''
    try {
      await authApi.logout()
      if (current !== attempt || batch !== sessionGeneration()) return false
      clearSession('anonymous', 'logout')
      return true
    } catch (cause) {
      if (current !== attempt || batch !== sessionGeneration()) return false
      if (isSessionError(cause)) {
        clearSession('anonymous', cause instanceof ApiError && cause.errorType === 'AUTH_FORBIDDEN' ? 'forbidden' : 'expired')
        return false
      }
      error.value = cause instanceof Error ? cause.message : '退出失败，请重试'
      return false
    } finally {
      submitting.value = false
    }
  }
  return { status: sessionStatus, user, authenticated, restoring, submitting, error, restore, login, logout }
})
