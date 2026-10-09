<template>
  <el-container v-if="$route.path !== '/login'" class="layout" :direction="isMobile ? 'vertical' : 'horizontal'">
    <!-- 移动端顶栏 + 抽屉菜单 -->
    <header v-if="isMobile" class="m-header">
      <span class="logo">🍯 keyHive</span>
      <el-button :icon="Menu" text size="large" aria-label="打开菜单" @click="drawer = true" />
    </header>
    <el-drawer v-model="drawer" direction="ltr" size="68%" :with-header="false" class="m-drawer">
      <div class="drawer-inner">
        <div class="logo" style="color: #1d2939">🍯 keyHive</div>
        <el-menu router :default-active="$route.path" @select="drawer = false">
          <el-menu-item index="/entries"><el-icon><Key /></el-icon><span>密钥条目</span></el-menu-item>
          <el-menu-item index="/templates"><el-icon><Files /></el-icon><span>录入模板</span></el-menu-item>
          <el-menu-item v-if="me.isAdmin" index="/users"><el-icon><User /></el-icon><span>用户管理</span></el-menu-item>
          <el-menu-item v-if="me.isAdmin" index="/tokens"><el-icon><Key /></el-icon><span>AI 令牌</span></el-menu-item>
          <el-menu-item v-if="me.isAdmin" index="/audit"><el-icon><Document /></el-icon><span>审计日志</span></el-menu-item>
          <el-menu-item index="/settings"><el-icon><Setting /></el-icon><span>设置</span></el-menu-item>
        </el-menu>
        <div class="logout" @click="drawer = false; logout()">退出登录</div>
      </div>
    </el-drawer>

    <!-- 桌面侧栏 -->
    <el-aside v-if="!isMobile" width="200px" class="aside">
      <div class="logo">🍯 keyHive</div>
      <el-menu router :default-active="$route.path" class="menu">
        <el-menu-item index="/entries">
          <el-icon><Key /></el-icon><span>密钥条目</span>
        </el-menu-item>
        <el-menu-item index="/templates">
          <el-icon><Files /></el-icon><span>录入模板</span>
        </el-menu-item>
        <el-menu-item v-if="me.isAdmin" index="/users">
          <el-icon><User /></el-icon><span>用户管理</span>
        </el-menu-item>
        <el-menu-item v-if="me.isAdmin" index="/tokens">
          <el-icon><Key /></el-icon><span>AI 令牌</span>
        </el-menu-item>
        <el-menu-item v-if="me.isAdmin" index="/audit">
          <el-icon><Document /></el-icon><span>审计日志</span>
        </el-menu-item>
        <el-menu-item index="/settings">
          <el-icon><Setting /></el-icon><span>设置</span>
        </el-menu-item>
      </el-menu>
      <div class="logout"><span class="who">{{ me.username }}{{ me.isAdmin ? '（管理员）' : '' }}</span><span class="act" @click="logout">退出登录</span></div>
    </el-aside>
    <el-main class="main"><router-view /></el-main>
    <ChatDrawer />
  </el-container>
  <router-view v-else />
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { Key, Files, Document, Setting, Menu, User } from '@element-plus/icons-vue'
import { api } from './api'
import { useIsMobile } from './ui'
import { me, loadMe } from './store'
import ChatDrawer from './components/ChatDrawer.vue'

const isMobile = useIsMobile()
const drawer = ref(false)

loadMe()

async function logout() {
  await api('/auth/logout', { method: 'POST' }).catch(() => {})
  location.href = '/login'
}
</script>

<style>
body { margin: 0; font-family: system-ui, 'Microsoft YaHei', sans-serif; background: #f5f7fa; }
.layout { min-height: 100vh; }
/* 桌面（水平布局）：固定视口高，el-main 自带 overflow:auto 独立滚动，
   侧栏与底部用户信息常驻可见，不随内容滚走 */
.layout:not(.is-vertical) { height: 100vh; }
.aside { background: #1d2939; color: #e5e7eb; display: flex; flex-direction: column; }
.logo { font-size: 18px; font-weight: 600; padding: 20px 16px; }
.menu { background: transparent; border-right: none; flex: 1; }
.menu .el-menu-item { color: #cbd5e1; }
.menu .el-menu-item.is-active { color: #60a5fa; background: #111827; }
.menu .el-menu-item:hover { background: #111827; }
.logout { padding: 16px; color: #94a3b8; font-size: 14px; display: flex; justify-content: space-between; gap: 8px; }
.logout .who { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.logout .act { cursor: pointer; flex-shrink: 0; }
.logout .act:hover { color: #f87171; }
.main { padding: 24px; }

/* 移动端顶栏 */
/* 移动端顶栏：sticky 跟随，滚动中也能随时打开菜单 */
.m-header { background: #1d2939; color: #fff; display: flex; align-items: center; justify-content: space-between; padding: 0 8px 0 16px; height: 52px; flex-shrink: 0; position: sticky; top: 0; z-index: 10; }
.m-header .logo { color: #fff; padding: 0; font-size: 17px; }
.m-header .el-button { color: #fff; }
.drawer-inner { display: flex; flex-direction: column; height: 100%; }
.drawer-inner .el-menu { border-right: none; flex: 1; }
.drawer-inner .logout { color: #64748b; }

@media (max-width: 768px) {
  .main { padding: 12px; }
  /* 弹窗与确认框小屏自适应 */
  .el-dialog { width: 94% !important; }
  .el-message-box { width: 92vw !important; max-width: 92vw; }
  /* 表格小屏字号略缩 */
  .el-table { font-size: 13px; }
}
</style>
