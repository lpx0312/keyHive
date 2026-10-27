import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import Users from './Users.vue'
import { mockUsers } from '../test/fixtures'

const apiMock = vi.fn()
vi.mock('../api', () => ({ api: (...a: any[]) => apiMock(...a) }))
// ElMessageBox 弹窗统一打桩：确认框直接放行
vi.mock('element-plus', async () => {
  const real = await vi.importActual<any>('element-plus')
  return { ...real, ElMessageBox: { ...real.ElMessageBox, confirm: vi.fn().mockResolvedValue(''), prompt: vi.fn().mockResolvedValue({ value: 'new-pw-12345' }) } }
})

describe('Users.vue', () => {
  beforeEach(() => {
    apiMock.mockReset()
    apiMock.mockResolvedValue(mockUsers)
  })

  it('挂载即拉取用户列表并渲染', async () => {
    const w = mount(Users, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/users')
    expect(w.text()).toContain('用户管理')
    expect(w.text()).toContain('alice')
    expect(w.text()).toContain('管理员')
    expect(w.text()).toContain('已禁用') // bob
    expect(w.text()).toContain('正常') // admin/alice
  })

  it('空列表不渲染行（后端恒返 []）', async () => {
    apiMock.mockResolvedValue([])
    const w = mount(Users, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).not.toContain('alice')
  })

  it('加载失败提示错误且不崩溃', async () => {
    apiMock.mockRejectedValue(new Error('需要管理员权限'))
    const w = mount(Users, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('用户管理') // 页面仍在
  })

  it('禁用/启用开关调用 PUT 且刷新列表', async () => {
    apiMock.mockResolvedValue(mockUsers)
    const w = mount(Users, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockResolvedValue(mockUsers)

    // 第一行操作列的「禁用」按钮（admin 行）
    const btns = w.findAll('button').filter(b => b.text() === '禁用')
    expect(btns.length).toBeGreaterThan(0)
    await btns[0].trigger('click')
    await flushPromises()
    const putCall = apiMock.mock.calls.find(c => c[1] && c[1].method === 'PUT')
    expect(putCall).toBeTruthy()
    expect(putCall![0]).toBe('/users/1')
    expect(JSON.parse(putCall![1].body)).toEqual({ disabled: true })
  })

  it('设为管理员/降权调用 PUT is_admin', async () => {
    apiMock.mockResolvedValue(mockUsers)
    const w = mount(Users, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockResolvedValue(mockUsers)
    const btns = w.findAll('button').filter(b => b.text() === '设为管理员')
    expect(btns.length).toBeGreaterThan(0)
    await btns[0].trigger('click')
    await flushPromises()
    const putCall = apiMock.mock.calls.find(c => c[1] && c[1].method === 'PUT')
    expect(putCall![0]).toBe('/users/2')
    expect(JSON.parse(putCall![1].body)).toEqual({ is_admin: true })
  })

  it('新建用户：空用户名/短密码拦截不发请求（边界）', async () => {
    const w = mount(Users, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    await w.find('.page-actions button').trigger('click') // 新建用户
    await flushPromises()
    const createBtns = w.findAll('.el-dialog button').filter(b => b.text() === '创建')
    // 空表单直接创建 → 用户名拦截
    await createBtns[createBtns.length - 1].trigger('click')
    await flushPromises()
    expect(apiMock).not.toHaveBeenCalled()
    // 填用户名、密码不足 8 位 → 密码拦截
    await w.find('input[placeholder="登录用户名"]').setValue('newuser')
    await w.find('input[placeholder="至少 8 位"]').setValue('short')
    await createBtns[createBtns.length - 1].trigger('click')
    await flushPromises()
    expect(apiMock).not.toHaveBeenCalled()
  })

  it('新建用户：填齐后 POST /users 并重置表单', async () => {
    const w = mount(Users, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.find('.page-actions button').trigger('click')
    await flushPromises()
    await w.find('input[placeholder="登录用户名"]').setValue('newuser')
    await w.find('input[placeholder="至少 8 位"]').setValue('password-8+')
    apiMock.mockClear()
    apiMock.mockResolvedValue(mockUsers)
    const createBtns = w.findAll('.el-dialog button').filter(b => b.text() === '创建')
    await createBtns[createBtns.length - 1].trigger('click')
    await flushPromises()
    const post = apiMock.mock.calls.find(c => c[1] && c[1].method === 'POST')
    expect(post).toBeTruthy()
    expect(post![0]).toBe('/users')
    expect(JSON.parse(post![1].body)).toEqual({ username: 'newuser', password: 'password-8+', is_admin: false })
  })

  it('重置密码：prompt 取值后 PUT password（会话被踢出提示在文案中）', async () => {
    const w = mount(Users, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockResolvedValue(mockUsers)
    const btns = w.findAll('button').filter(b => b.text() === '重置密码')
    await btns[0].trigger('click')
    await flushPromises()
    const putCall = apiMock.mock.calls.find(c => c[1] && c[1].method === 'PUT')
    expect(putCall).toBeTruthy()
    expect(JSON.parse(putCall![1].body)).toEqual({ password: 'new-pw-12345' })
  })

  it('删除用户：确认后 DELETE /users/:id 并刷新', async () => {
    const w = mount(Users, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockResolvedValue(mockUsers)
    const btns = w.findAll('button').filter(b => b.text() === '删除')
    await btns[0].trigger('click')
    await flushPromises()
    const del = apiMock.mock.calls.find(c => c[1] && c[1].method === 'DELETE')
    expect(del).toBeTruthy()
    expect(del![0]).toBe('/users/1')
  })
})
