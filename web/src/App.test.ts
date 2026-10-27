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
})
