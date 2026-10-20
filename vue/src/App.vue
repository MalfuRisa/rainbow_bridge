<script setup lang="ts">
import { Toaster } from 'vue-sonner'
import { useAuthStore } from '@/stores/auth'
import AuthLoading from '@/components/auth/AuthLoading.vue'
import AuthRecovery from '@/components/auth/AuthRecovery.vue'
const auth = useAuthStore()
</script>

<template>
  <div class="min-h-screen bg-background text-foreground">
    <AuthLoading v-if="auth.restoring && !$route.matched.length" />
    <AuthRecovery v-else-if="auth.status === 'failed' && !['/', '/login'].includes($route.path)" />
    <RouterView v-else />
    <Toaster position="top-center" :richColors="true" />
  </div>
</template>
