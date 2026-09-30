import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import App from './App.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('./views/Login.vue') },
    { path: '/', redirect: '/entries' },
    { path: '/entries', component: () => import('./views/EntriesList.vue') },
    { path: '/entries/new', component: () => import('./views/EntryEdit.vue') },
    { path: '/entries/:id/edit', component: () => import('./views/EntryEdit.vue') },
    { path: '/templates', component: () => import('./views/Templates.vue') },
    { path: '/tokens', component: () => import('./views/Tokens.vue') },
    { path: '/audit', component: () => import('./views/Audit.vue') },
    { path: '/users', component: () => import('./views/Users.vue') },
    { path: '/settings', component: () => import('./views/Settings.vue') },
  ],
})

createApp(App).use(router).use(ElementPlus, { locale: zhCn }).mount('#app')
