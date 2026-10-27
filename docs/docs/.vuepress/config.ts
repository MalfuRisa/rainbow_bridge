import { defineUserConfig } from 'vuepress'
import { defaultTheme } from '@vuepress/theme-default'
import { searchPlugin } from '@vuepress/plugin-search'
import { viteBundler } from '@vuepress/bundler-vite'

export default defineUserConfig({
  lang: 'zh-CN',
  title: '虹桥计划',
  description: '虹桥计划（Rainbow Bridge）技术文档 - 静态资源与配置管理中台',
  
  base: '/rainbow_bridge/',
  
  bundler: viteBundler(),
  
  head: [
    ['link', { rel: 'icon', href: '/icon.svg' }],
    ['meta', { name: 'viewport', content: 'width=device-width,initial-scale=1' }],
  ],

  theme: defaultTheme({
    logo: '/icon.svg',
    repo: 'yi-nology/rainbow_bridge',
    docsRepo: 'yi-nology/rainbow_bridge',
    docsDir: 'docs',
    docsBranch: 'main',
    editLinkText: '在 GitHub 上编辑此页',
    lastUpdatedText: '上次更新',
    
    navbar: [
      { text: '首页', link: '/' },
      { 
        text: '使用指南', 
        link: '/guide/',
        activeMatch: '/guide/'
      },
      { 
        text: '工程状态', 
        link: '/status/',
        activeMatch: '/status/'
      },
      { 
        text: '发版本', 
        link: '/release/',
        activeMatch: '/release/'
      },
      { 
        text: 'API', 
        link: '/api/',
        activeMatch: '/api/'
      },
    ],

    sidebar: {
      '/guide/': [
        {
          text: '快速开始',
          children: [
            '/guide/',
            '/guide/installation',
            '/guide/quick-start',
          ],
        },
        {
          text: 'UI配置流程',
          children: [
            '/guide/ui-config/environment',
            '/guide/ui-config/channel',
            '/guide/ui-config/config',
            '/guide/ui-config/resource',
            '/guide/ui-config/export',
            '/guide/ui-config/migration',
          ],
        },
        {
          text: '部署配置',
          children: [
            '/guide/reverse-proxy',
          ],
        },
        {
          text: '平台对接',
          children: [
            '/guide/integration/frontend',
          ],
        },
      ],
      '/status/': [
        {
          text: '工程可用状态',
          children: [
            '/status/',
          ],
        },
      ],
      '/release/': [
        {
          text: '发版本信息',
          children: [
            '/release/',
          ],
        },
      ],
      '/api/': [
        {
          text: 'API 接口',
          children: [
            '/api/',
            '/api/scope-identity',
            '/api/transfer-export',
            '/api/release-compare',
            '/api/backup',
          ],
        },
      ],
    },
  }),

  plugins: [
    searchPlugin({
      locales: {
        '/': {
          placeholder: '搜索文档',
        },
      },
      maxSuggestions: 10,
    }),
  ],

  markdown: {
    anchor: {
      permalink: true,
      permalinkBefore: true,
      permalinkSymbol: '#',
    },
  },
})
