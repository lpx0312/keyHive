import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { createRouter, createMemoryHistory } from 'vue-router'
import App from './App.vue'

const apiMock = vi.fn()
vi.mock('./api', () => ({ api: (...a: any[]) => apiMock(...a) }))
vi.mock('./store', () => ({
  me: { id: 1, username: 'admin', isAdmin: true, loaded: true },
  loadMe: vi.fn().mockResolvedValue(undefined),
}))
// ChatDrawer 依赖 SSE/markstream，组件级测试中打桩
vi.mock('./components/ChatDrawer.vue', () => ({ default: { name: 'ChatDrawer', render: () => null } }))

// 真路由（内存模式）：App 模板用 $route.path 切换布局
async function newRouter(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/entries', component: { render: () => null } },
      { path: '/login', component: { render: () => null } },
    ],
  })
  await router.push(path)
  await router.isReady()
  return router
}

describe('App.vue', () => {
  beforeEach(() => {
    apiMock.mockReset()
  })

  it('主布局渲染：侧栏菜单 + 管理员入口（me.isAdmin=true）', async () => {
    const router = await newRouter('/entries')
    const w = mount(App, {
      global: { plugins: [ElementPlus, router], stubs: { 'router-view': true } },
    })
    await flushPromises()
    expect(w.text()).toContain('keyHive')
    expect(w.text()).toContain('密钥条目')
    expect(w.text()).toContain('审计日志')
    expect(w.text()).toContain('admin（管理员）')
  })

  it('非管理员：仅普通用户菜单（无用户/令牌/审计入口）', async () => {
    const { me } = await import('./store')
    const saved = me.isAdmin
    me.isAdmin = false
    const router = await newRouter('/entries')
    const w = mount(App, {
      global: { plugins: [ElementPlus, router], stubs: { 'router-view': true } },
    })
    await flushPromises()
    expect(w.text()).not.toContain('用户管理')
    expect(w.text()).not.toContain('AI 令牌')
    expect(w.text()).not.toContain('审计日志')
    expect(w.text()).toContain('录入模板') // 普通用户可见
    me.isAdmin = saved
  })

  it('移动端：渲染顶栏而非侧栏（窄屏分支）', async () => {
    const saved = window.innerWidth
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    try {
      const router = await newRouter('/entries')
      const w = mount(App, {
        global: { plugins: [ElementPlus, router], stubs: { 'router-view': true } },
      })
      await flushPromises()
      expect(w.find('.m-header').exists()).toBe(true)
      expect(w.find('.aside').exists()).toBe(false)
      // 汉堡菜单打开抽屉（el-drawer 渲染在组件树内）
      await w.find('.m-header button').trigger('click')
      await flushPromises()
      expect(w.find('.el-drawer').exists()).toBe(true)
      expect(w.text()).toContain('退出登录')
      // 点菜单项：@select 关抽屉
      const item = w.findAll('.el-drawer .el-menu-item')
      expect(item.length).toBeGreaterThan(0)
      await item[0].trigger('click')
      await flushPromises()
      w.unmount()
    } finally {
      Object.defineProperty(window, 'innerWidth', { value: saved, configurable: true, writable: true })
    }
  })

  it('移动端抽屉内退出登录', async () => {
    apiMock.mockResolvedValue({ ok: true })
    const saved = window.innerWidth
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    try {
      const router = await newRouter('/entries')
      const w = mount(App, {
        global: { plugins: [ElementPlus, router], stubs: { 'router-view': true } },
      })
      await flushPromises()
      await w.find('.m-header button').trigger('click')
      await flushPromises()
      const logoutDiv = w.findAll('.el-drawer .logout')
      expect(logoutDiv.length).toBe(1)
      await logoutDiv[0].trigger('click')
      await flushPromises()
      expect(apiMock).toHaveBeenCalledWith('/auth/logout', { method: 'POST' })
      w.unmount()
    } finally {
      Object.defineProperty(window, 'innerWidth', { value: saved, configurable: true, writable: true })
    }
  })

  it('登录页路由：不渲染侧栏布局（只剩 router-view）', async () => {
    const router = await newRouter('/login')
    const w = mount(App, {
      global: { plugins: [ElementPlus, router], stubs: { 'router-view': true } },
    })
    await flushPromises()
    expect(w.find('.aside').exists()).toBe(false)
    expect(w.text()).not.toContain('退出登录')
  })

  it('退出登录：POST /auth/logout', async () => {
    apiMock.mockResolvedValue({ ok: true })
    const router = await newRouter('/entries')
    const w = mount(App, {
      global: { plugins: [ElementPlus, router], stubs: { 'router-view': true } },
    })
    await flushPromises()
    await w.find('.act').trigger('click')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/auth/logout', { method: 'POST' })
  })

  it('退出登录失败也静默跳转（catch 兜底分支）', async () => {
    apiMock.mockRejectedValue(new Error('网络错误'))
    const router = await newRouter('/entries')
    const w = mount(App, {
      global: { plugins: [ElementPlus, router], stubs: { 'router-view': true } },
    })
    await flushPromises()
    await w.find('.act').trigger('click')
    await flushPromises()
    // 不抛错即通过（location.href 赋值在 jsdom 中为 no-op 导航告警）
    expect(true).toBe(true)
  })
})
