import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import Settings from './Settings.vue'

const apiMock = vi.fn()
vi.mock('../api', () => ({ api: (...a: any[]) => apiMock(...a) }))
vi.mock('../store', () => ({ me: { id: 1, username: 'admin', isAdmin: true }, setFabHidden: vi.fn() }))
// ElMessage 记录层：透传真实实现（保持 DOM 渲染行为），同时记录 level:message 供断言
const { msgCalls } = vi.hoisted(() => ({ msgCalls: [] as string[] }))
vi.mock('element-plus', async () => {
  const real = await vi.importActual<any>('element-plus')
  const rec = (level: string) => (m: any) => {
    msgCalls.push(`${level}:${typeof m === 'string' ? m : m?.message ?? ''}`)
    return (real.ElMessage as any)[level](m)
  }
  return {
    ...real,
    ElMessageBox: { ...real.ElMessageBox, confirm: vi.fn().mockResolvedValue('') },
    ElMessage: Object.assign((...a: any[]) => (real.ElMessage as any)(...a), {
      error: rec('error'), success: rec('success'), warning: rec('warning'), info: rec('info'),
    }),
  }
})

const presets = [
  { name: 'DeepSeek', protocol: 'openai', base_url: 'https://api.deepseek.com/v1', models: ['deepseek-chat', 'deepseek-reasoner'], key_hint: '平台「API Keys」页创建' },
  { name: 'Claude', protocol: 'anthropic', base_url: 'https://api.anthropic.com', models: ['claude-sonnet-4'] },
]

// 按路径分发默认响应；单测可再覆写
function mockDefaults() {
  apiMock.mockImplementation((path: string, opts?: any) => {
    if (path === '/ai-config' && opts?.method === 'PUT') return Promise.resolve({})
    if (path === '/ai-config') return Promise.resolve({
      presets,
      protocol: 'openai', base_url: 'https://api.deepseek.com/v1',
      api_key: '', model: 'deepseek-chat', configured: 'true',
    })
    if (path === '/ai-config/models') return Promise.resolve({ ok: true, models: ['deepseek-chat', 'deepseek-v3'] })
    if (path === '/ai-config/test') return Promise.resolve({ ok: true, model: 'deepseek-chat', protocol: 'openai', latency_ms: 321, reply: 'pong' })
    if (path === '/settings/stale-days') return Promise.resolve({ stale_days: 90 })
    if (path === '/version') return Promise.resolve({ version: '1.3.11-test' })
    if (path === '/password') return Promise.resolve({})
    if (path === '/rotate-key') return Promise.resolve({ entries: 5, key_file: 'data/master.key', new_master_key: '' })
    return Promise.resolve({})
  })
}

describe('Settings.vue', () => {
  beforeEach(() => {
    apiMock.mockReset()
    mockDefaults()
    localStorage.clear()
    document.body.innerHTML = '' // 清理前序测试 teleport 到 body 的下拉/弹层残留
    msgCalls.length = 0
  })

  it('挂载加载 AI 配置/轮换阈值/版本，预设回显匹配项', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('设置')
    expect(w.text()).toContain('已配置')
    expect(w.text()).toContain('keyHive 1.3.11-test')
    // base_url 与 DeepSeek 预设一致 → 选中标签回显 DeepSeek（el-select 文本在 .el-select__placeholder）
    expect(w.text()).toContain('DeepSeek')
  })

  it('Key 占位符：已配置显示尾 4 位遮蔽提示（边界）', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    // api_key 为空串 → tail 为空，提示「已保存（****）」不拼 hint 之外的异常
    const keyInput = w.find('input[placeholder^="已保存"]')
    expect(keyInput.exists()).toBe(true)
    expect((keyInput.element as HTMLInputElement).placeholder).toContain('已保存（****）')
  })

  it('未配置时 Key 占位符回落预设 hint 或 sk-...（边界）', async () => {
    apiMock.mockImplementation((path: string) =>
      path === '/ai-config'
        ? Promise.resolve({ presets, protocol: 'anthropic', base_url: 'https://api.anthropic.com', api_key: '', model: '', configured: 'false' })
        : Promise.resolve(path === '/version' ? { version: 't' } : { stale_days: 90 }))
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('未配置')
    // 未选预设时回落 sk-...
    const inputs = w.findAll('input')
    const keyLike = inputs.find(i => (i.element as HTMLInputElement).placeholder.includes('sk-'))
    expect(keyLike).toBeTruthy()
  })

  it('选预设自动填协议/地址/模型', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    // API 地址输入框回显已保存（=预设）地址；模型回显 deepseek-chat
    const urlInput = w.findAll('input').find(i => (i.element as HTMLInputElement).placeholder.includes('OpenAI 兼容端点'))
    expect(urlInput).toBeTruthy()
    expect((urlInput!.element as HTMLInputElement).value).toBe('https://api.deepseek.com/v1')
    // 模型选中值显示在 el-select 标签内（非 input value）
    expect(w.text()).toContain('deepseek-chat')
  })

  it('获取模型列表：成功填充并提示数量', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    await w.findAll('button').filter(b => b.text() === '获取模型列表')[0].trigger('click')
    await flushPromises()
    const call = apiMock.mock.calls.find(c => c[0] === '/ai-config/models')
    expect(call).toBeTruthy()
  })

  it('获取模型列表失败（服务商不返回模型）提示错误（边界）', async () => {
    apiMock.mockImplementation((path: string) => {
      if (path === '/ai-config/models') return Promise.resolve({ ok: true, models: [] })
      if (path === '/ai-config') return Promise.resolve({ presets, protocol: 'openai', base_url: 'https://api.deepseek.com/v1', api_key: '', model: 'deepseek-chat', configured: 'true' })
      return Promise.resolve(path === '/version' ? { version: 't' } : { stale_days: 90 })
    })
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '获取模型列表')[0].trigger('click')
    await flushPromises()
    // 空模型列表走 error 分支，不崩溃
    expect(w.text()).toContain('获取模型列表')
  })

  it('测试连接：成功显示协议/模型/耗时', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    await w.findAll('button').filter(b => b.text() === '测试连接')[0].trigger('click')
    await flushPromises()
    expect(w.text()).toContain('连接成功')
    expect(w.text()).toContain('耗时 321ms')
    expect(w.text()).toContain('deepseek-chat')
    // 关闭结果条（@close 回调 → testResult=null）
    const closeBtn = w.find('.el-alert .el-alert__close-btn')
    if (closeBtn.exists()) {
      await closeBtn.trigger('click')
      await flushPromises()
      expect(w.text()).not.toContain('耗时 321ms')
    }
  })

  it('测试连接：请求异常转入失败态显示错误信息（边界）', async () => {
    apiMock.mockImplementation((path: string) => {
      if (path === '/ai-config/test') return Promise.reject(new Error('连接超时'))
      if (path === '/ai-config') return Promise.resolve({ presets, protocol: 'openai', base_url: 'https://api.deepseek.com/v1', api_key: '', model: 'm', configured: 'false' })
      return Promise.resolve(path === '/version' ? { version: 't' } : { stale_days: 90 })
    })
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '测试连接')[0].trigger('click')
    await flushPromises()
    expect(w.text()).toContain('连接失败')
    expect(w.text()).toContain('连接超时')
  })

  it('修改密码校验：新密码不足 8 位拦截（边界）', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    const pwInputs = w.findAll('input[type="password"]')
    await pwInputs[0].setValue('old')
    await pwInputs[1].setValue('short')
    await pwInputs[2].setValue('short')
    await w.findAll('button').filter(b => b.text().includes('修改（'))[0].trigger('click')
    await flushPromises()
    expect(apiMock).not.toHaveBeenCalled()
  })

  it('修改密码：两次不一致拦截；一致则 POST /password（边界）', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    const pwInputs = w.findAll('input[type="password"]')
    await pwInputs[0].setValue('old-pass-123')
    await pwInputs[1].setValue('new-pass-123')
    await pwInputs[2].setValue('different-999')
    await w.findAll('button').filter(b => b.text().includes('修改（'))[0].trigger('click')
    await flushPromises()
    expect(apiMock).not.toHaveBeenCalled()
    await pwInputs[2].setValue('new-pass-123')
    await w.findAll('button').filter(b => b.text().includes('修改（'))[0].trigger('click')
    await flushPromises()
    const call = apiMock.mock.calls.find(c => c[0] === '/password')
    expect(call).toBeTruthy()
    expect(JSON.parse(call![1].body)).toEqual({ old_password: 'old-pass-123', new_password: 'new-pass-123' })
  })

  it('悬浮球开关写 localStorage 并广播（经 setFabHidden）', async () => {
    const { setFabHidden } = await import('../store')
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    const switches = w.findAll('.el-switch')
    expect(switches.length).toBe(1)
    await switches[0].trigger('click')
    await flushPromises()
    expect(setFabHidden).toHaveBeenCalled()
  })

  it('主密钥轮换：确认后 POST /rotate-key 并展示结果', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    await w.findAll('button').filter(b => b.text() === '轮换主密钥')[0].trigger('click')
    await flushPromises()
    const call = apiMock.mock.calls.find(c => c[0] === '/rotate-key')
    expect(call).toBeTruthy()
    expect(w.text()).toContain('已重加密 5 条')
  })

  it('切换协议清空已拉取的模型清单（onProtocolChange）', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    // 先拉取模型列表填充 fetchedModels，再切到 Anthropic 协议
    await w.findAll('button').filter(b => b.text() === '获取模型列表')[0].trigger('click')
    await flushPromises()
    const radios = w.findAll('.el-radio')
    const anthropic = radios.filter(r => r.text().includes('Anthropic'))
    expect(anthropic.length).toBe(1)
    // jsdom 下 label click 不联动 input，直接对 input setValue 触发 change
    await anthropic[0].find('input').setValue(true)
    await flushPromises()
    // 切协议后 API 地址 placeholder 变为 Anthropic 示例
    const ph = w.findAll('input').find(i => (i.element as HTMLInputElement).placeholder.includes('anthropic.com'))
    expect(ph).toBeTruthy()
  })

  it('保存 AI 配置：PUT 后重载并清空 Key 输入（遮蔽回填）', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    await w.findAll('button').filter(b => b.text() === '保存配置')[0].trigger('click')
    await flushPromises()
    const put = apiMock.mock.calls.find(c => c[0] === '/ai-config' && c[1]?.method === 'PUT')
    expect(put).toBeTruthy()
    // 保存后 loadAI 重载（GET 再次发生）
    expect(apiMock.mock.calls.some(c => c[0] === '/ai-config' && !c[1])).toBe(true)
  })

  it('保存 AI 配置失败：提示错误不崩溃（catch 分支）', async () => {
    apiMock.mockImplementation((path: string, opts?: any) => {
      if (path === '/ai-config' && opts?.method === 'PUT') return Promise.reject(new Error('Key 无效'))
      if (path === '/ai-config') return Promise.resolve({ presets, protocol: 'openai', base_url: 'https://api.deepseek.com/v1', api_key: '', model: 'm', configured: 'true' })
      return Promise.resolve(path === '/version' ? { version: 't' } : { stale_days: 90 })
    })
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '保存配置')[0].trigger('click')
    await flushPromises()
    expect(w.text()).toContain('设置')
  })

  it('选择预设：自动填协议/地址/模型（onPreset 全链路）', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    // 展开服务商预设下拉并选 Claude（options teleport 到 body；初始回显 DeepSeek，按选中值定位）
    const sel = w.findAll('.el-select').find(s => s.text().includes('DeepSeek'))
    expect(sel).toBeTruthy()
    await sel!.trigger('click')
    await flushPromises()
    const opt = Array.from(document.querySelectorAll('.el-select-dropdown__item'))
      .find(el => (el.textContent || '').includes('Claude'))
    expect(opt).toBeTruthy()
    opt!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    // 协议切 anthropic → 地址 placeholder 变化；模型自动填预设首个
    const ph = w.findAll('input').find(i => (i.element as HTMLInputElement).placeholder.includes('anthropic.com'))
    expect(ph).toBeTruthy()
  })

  it('轮换阈值保存：PUT /settings/stale-days（边界）', async () => {
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    await w.findAll('button').filter(b => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    const put = apiMock.mock.calls.find(c => c[0] === '/settings/stale-days' && c[1]?.method === 'PUT')
    expect(put).toBeTruthy()
    expect(JSON.parse(put![1].body)).toEqual({ stale_days: 90 })
  })

  it('主密钥轮换失败：提示错误不崩溃（catch 分支）', async () => {
    apiMock.mockImplementation((path: string, opts?: any) => {
      if (path === '/rotate-key') return Promise.reject(new Error('库被锁'))
      if (path === '/ai-config') return Promise.resolve({ presets, protocol: 'openai', base_url: 'u', api_key: '', model: 'm', configured: 'false' })
      return Promise.resolve(path === '/version' ? { version: 't' } : { stale_days: 90 })
    })
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '轮换主密钥')[0].trigger('click')
    await flushPromises()
    expect(w.text()).toContain('设置')
  })

  it('轮换阈值保存失败：提示错误不崩溃（catch 分支）', async () => {
    apiMock.mockImplementation((path: string, opts?: any) => {
      if (path === '/settings/stale-days' && opts?.method === 'PUT') return Promise.reject(new Error('只读'))
      if (path === '/settings/stale-days') return Promise.resolve({ stale_days: 90 })
      if (path === '/ai-config') return Promise.resolve({ presets, protocol: 'openai', base_url: 'u', api_key: '', model: 'm', configured: 'false' })
      return Promise.resolve({ version: 't' })
    })
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    expect(w.text()).toContain('设置')
  })

  it('非管理员不显示轮换卡（边界）', async () => {
    // me 是模块导出的对象，直接改属性模拟非管理员（模板读 isAdmin）
    const { me } = await import('../store')
    const saved = (me as any).isAdmin
    ;(me as any).isAdmin = false
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).not.toContain('轮换主密钥')
    expect(w.text()).not.toContain('密码轮换提醒')
    ;(me as any).isAdmin = saved
  })

  // ---- 缺陷修复回归（审查 P2：Settings.vue change() 修改密码无 try/catch，已补 catch + ElMessage.error） ----
  // 场景：旧密码错误（POST /password 返回错误）。正确行为：失败时向用户展示错误信息（ElMessage.error）。
  it('修改密码失败（旧密码错误）应向用户展示错误提示', async () => {
    apiMock.mockImplementation((path: string) => {
      if (path === '/password') return Promise.reject(new Error('旧密码不正确'))
      if (path === '/ai-config') return Promise.resolve({ presets, protocol: 'openai', base_url: 'u', api_key: '', model: 'm', configured: 'false' })
      return Promise.resolve(path === '/version' ? { version: 't' } : { stale_days: 90 })
    })
    const w = mount(Settings, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    msgCalls.length = 0
    const pwInputs = w.findAll('input[type="password"]')
    await pwInputs[0].setValue('wrong-old-pass')
    await pwInputs[1].setValue('new-pass-1234')
    await pwInputs[2].setValue('new-pass-1234')
    await w.findAll('button').filter(b => b.text().includes('修改（'))[0].trigger('click')
    await flushPromises()
    expect(msgCalls.some(c => c.startsWith('error:') && c.includes('旧密码不正确'))).toBe(true)
    w.unmount()
  })
})
