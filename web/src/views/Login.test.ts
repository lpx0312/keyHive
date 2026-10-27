import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import Login from './Login.vue'

// mock api 与 store（loadMe）
const apiMock = vi.fn()
const loadMeMock = vi.fn().mockResolvedValue(undefined)
vi.mock('../api', () => ({ api: (...a: any[]) => apiMock(...a) }))
vi.mock('../store', () => ({ loadMe: () => loadMeMock() }))
const pushMock = vi.fn()
vi.mock('vue-router', () => ({ useRouter: () => ({ push: pushMock }) }))

// ElMessage 是命名导入，mock element-plus 太重；直接放行真实组件

describe('Login.vue', () => {
  beforeEach(() => {
    apiMock.mockReset()
    pushMock.mockReset()
    loadMeMock.mockClear()
  })

  it('渲染登录表单与标题', () => {
    const w = mount(Login, { global: { plugins: [ElementPlus] } })
    expect(w.text()).toContain('keyHive 密钥管家')
    expect(w.find('button').exists()).toBe(true)
  })

  it('成功登录：调 api → loadMe → 跳 /entries', async () => {
    apiMock.mockResolvedValue({ username: 'admin' })
    const w = mount(Login, { global: { plugins: [ElementPlus] } })
    // 填表单
    const inputs = w.findAll('input')
    await inputs[0].setValue('admin')
    await inputs[1].setValue('e2e-pw')
    await w.find('button').trigger('submit')
    await w.vm.$nextTick()
    await flushPromises()

    expect(apiMock).toHaveBeenCalledWith('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: 'admin', password: 'e2e-pw' }),
    })
    expect(loadMeMock).toHaveBeenCalledTimes(1)
    expect(pushMock).toHaveBeenCalledWith('/entries')
    // loading 复位
    expect(w.text()).not.toContain('loading')
  })

  it('登录失败：不跳转（错误由 ElMessage 提示）', async () => {
    apiMock.mockRejectedValue(new Error('用户名或密码错误'))
    const w = mount(Login, { global: { plugins: [ElementPlus] } })
    const inputs = w.findAll('input')
    await inputs[0].setValue('admin')
    await inputs[1].setValue('bad-pw')
    await w.find('button').trigger('submit')
    await flushPromises()

    expect(pushMock).not.toHaveBeenCalled()
    expect(w.vm.$.isMounted).toBe(true)
  })
})
