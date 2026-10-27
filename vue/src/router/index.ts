import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { safeReturnTo } from '@/lib/auth/redirect'

const routes: RouteRecordRaw[] = [
  { path: '/backups', name: 'Backups', component: () => import('@/views/BackupsPage.vue') },
  { path: '/login', name: 'Login', component: () => import('@/views/LoginPage.vue') },
  {
    path: '/',
    name: 'Home',
    component: () => import('@/views/HomePage.vue'),
  },
  {
    path: '/config',
    name: 'Config',
    component: () => import('@/views/ConfigPage.vue'),
  },
  {
    path: '/environments',
    name: 'Environments',
    component: () => import('@/views/EnvironmentsPage.vue'),
  },
  {
    path: '/resources',
    name: 'Resources',
    component: () => import('@/views/ResourcesPage.vue'),
  },
  {
    path: '/migration',
    name: 'Migration',
    component: () => import('@/views/MigrationPage.vue'),
  },
  {
    path: '/import-export',
    name: 'ImportExport',
    component: () => import('@/views/ImportExportPage.vue'),
  },
  {
    path: '/releases',
    name: 'Releases',
    component: () => import('@/views/ReleasesPage.vue'),
  },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
  scrollBehavior(to, from, savedPosition) {
    if (savedPosition) return savedPosition
    if (to.path !== from.path) return { top: 0 }
    return false
  },
})

router.beforeEach(async to => {
  const auth = useAuthStore()
  if (to.path === '/') {
    void auth.restore()
    return true
  }
  await auth.restore()
  if (to.path === '/login') return auth.authenticated ? safeReturnTo(to.query.redirect) : true
  return auth.authenticated ? true : { path: '/login', query: { redirect: safeReturnTo(to.fullPath) } }
})

export default router
