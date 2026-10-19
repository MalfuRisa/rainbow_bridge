import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
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
})

export default router
