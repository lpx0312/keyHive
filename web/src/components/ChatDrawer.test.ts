// ChatDrawer 最小覆盖：挂载渲染（悬浮球/抽屉标题/输入区）、显隐联动（localStorage + FAB_HIDDEN_EVENT）、
// SSE 消费主链路（reply_delta / draft / 敏感遮蔽 / done）、草稿入库与清空对话。
// 说明：该组件无 props（自包含 + 全局事件驱动），"props 变化响应"以等价的外部状态输入
// （localStorage kh-fab-hidden 与 FAB_HIDDEN_EVENT 广播）代替。
// 注：vitest.config 的 coverage.exclude 仍排除 ChatDrawer.vue（维持既有口径，由本文件保证行为回归）。
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import ElementPlus from 'element-plus'
import ChatDrawer from './ChatDrawer.vue'

const apiMock = vi.fn()
const notifyMock = vi.fn()
vi.mock('../api', () => ({ api: (...a: any[]) => apiMock(...a) }))
vi.mock('../store', () => ({
  notifyEntriesChanged: (...a: any[]) => notifyMock(...a),
  FAB_HIDDEN_EVENT: 'keyhive:fab-hidden-changed',
}))
vi.mock('../ui', () => ({ copyText: vi.fn().mockResolvedValue(true) }))

const draftEntry = {
  id: 0,
  title: '华为云 SWR',
  category: 'docker_registry',
  tags: [],
  description: '',
  ai_visible: true,
  fields: [
    { key: 'username', description: '用户名', type: 'text', is_secret: false, value: 'ci-bot' },
    { key: 'password', description: '密码', type: 'text', is_secret: true, value: 'super-secret-123' },
  ],
  created_at: '2026-10-28T00:00:00Z',
  updated_at: '2026-10-28T00:00:00Z',
}

// 构造 /ai-chat/stream 的 SSE 响应体（与后端帧格式一致：`data: {...}\n\n`）
function sseBody(events: any[]): ReadableStream<Uint8Array> {
  const enc = new TextEncoder()
  const payload = events.map((ev) => `data: ${JSON.stringify(ev)}\n\n`).join('')
  return new ReadableStream({
    start(c) {
      c.enqueue(enc.encode(payload))
      c.close()
    },
  })
}

function stubSSE(events: any[]) {
  const fetchMock = vi.fn(async () => ({ status: 200, ok: true, body: sseBody(events) }))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

// jsdom 未实现 PointerEvent / Pointer Capture：用 MouseEvent 派发 + 原型打桩
function pointer(el: Element, type: 'pointerdown' | 'pointerup') {
  el.dispatchEvent(new MouseEvent(type, { bubbles: true, clientX: 10, clientY: 10 }))
}

async function openDrawer(w: ReturnType<typeof mount>) {
  const fab = w.find('.chat-fab')
  pointer(fab.element, 'pointerdown')
  pointer(fab.element, 'pointerup')
  // el-drawer append-to-body teleport，渲染异步：waitFor 等标题出现
  await vi.waitFor(() => expect(document.querySelector('.dtitle')).toBeTruthy())
}

function bodyText() { return document.body.textContent || '' }
function bodyButtons() { return Array.from(document.querySelectorAll('button')) }
function findButton(text: string) {
  return bodyButtons().find((b) => (b.textContent || '').includes(text)) as HTMLButtonElement | undefined
}

describe('ChatDrawer.vue', () => {
  beforeEach(() => {
    localStorage.clear()
    document.body.innerHTML = '' // mount 前清 teleport 残留（既有坑）
    apiMock.mockReset()
    apiMock.mockResolvedValue({ id: 1 })
    notifyMock.mockReset()
    ;(HTMLElement.prototype as any).setPointerCapture = vi.fn()
    ;(HTMLElement.prototype as any).releasePointerCapture = vi.fn()
  })
  afterEach(() => {
    document.body.innerHTML = ''
    vi.unstubAllGlobals()
    delete (HTMLElement.prototype as any).setPointerCapture
    delete (HTMLElement.prototype as any).releasePointerCapture
  })

  it('挂载渲染悬浮球；localStorage kh-fab-hidden=1 时不渲染', () => {
    const w = mount(ChatDrawer, { global: { plugins: [ElementPlus] } })
    expect(w.find('.chat-fab').exists()).toBe(true)
    w.unmount()

    localStorage.setItem('kh-fab-hidden', '1')
    const w2 = mount(ChatDrawer, { global: { plugins: [ElementPlus] } })
    expect(w2.find('.chat-fab').exists()).toBe(false)
    w2.unmount()
  })

  it('FAB_HIDDEN_EVENT 广播后悬浮球隐藏（设置页开关的跨 chunk 同步）', async () => {
    const w = mount(ChatDrawer, { global: { plugins: [ElementPlus] } })
    expect(w.find('.chat-fab').exists()).toBe(true)
    // 真实链路：setFabHidden 先写 localStorage 再广播；syncHidden 从 localStorage 读真值
    localStorage.setItem('kh-fab-hidden', '1')
    window.dispatchEvent(new Event('keyhive:fab-hidden-changed'))
    await nextTick()
    expect(w.find('.chat-fab').exists()).toBe(false)
    // 重新开启：写 0 再广播 → 恢复显示
    localStorage.setItem('kh-fab-hidden', '0')
    window.dispatchEvent(new Event('keyhive:fab-hidden-changed'))
    await nextTick()
    expect(w.find('.chat-fab').exists()).toBe(true)
    w.unmount()
  })

  it('右键悬浮球隐藏并写 localStorage（恢复入口在设置页）', async () => {
    const w = mount(ChatDrawer, { global: { plugins: [ElementPlus] } })
    await w.find('.chat-fab').trigger('contextmenu')
    await nextTick()
    expect(w.find('.chat-fab').exists()).toBe(false)
    expect(localStorage.getItem('kh-fab-hidden')).toBe('1')
    w.unmount()
  })

  it('原地点击悬浮球打开抽屉：标题/隐私提示/欢迎语/输入区渲染（teleport 到 body）', async () => {
    const w = mount(ChatDrawer, { global: { plugins: [ElementPlus] } })
    await openDrawer(w)
    expect((document.querySelector('.dtitle') as HTMLElement).textContent).toContain('AI 录入助手')
    expect(bodyText()).toContain('发送到你配置的 LLM 服务商')
    expect(bodyText()).toContain('大白话') // 欢迎语
    expect(document.querySelector('textarea')).toBeTruthy() // 输入区
    expect((document.querySelector('textarea') as HTMLTextAreaElement).placeholder).toContain('用自然语言描述凭据')
    expect(findButton('发送（Enter）')).toBeTruthy()
    expect(findButton('清空对话')).toBeTruthy()
    w.unmount()
  })

  it('发送消息消费 SSE：用户消息上屏、status/reply 渲染、草稿卡敏感值遮蔽、loading 复位', async () => {
    const fetchMock = stubSSE([
      { type: 'status', text: '正在解析' },
      { type: 'reply_delta', text: '已解析出条目，请确认' },
      { type: 'draft', draft: draftEntry, draft_kind: 'create', draft_entry_id: 0 },
      { type: 'done', text: '' },
    ])
    const w = mount(ChatDrawer, { global: { plugins: [ElementPlus] } })
    await openDrawer(w)
    const ta = document.querySelector('textarea') as HTMLTextAreaElement
    ta.value = '存一个华为云 SWR 凭据'
    ta.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    findButton('发送（Enter）')!.click()
    await flushPromises()

    // 请求体带用户输入与 history（首条为欢迎语）
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const reqBody = JSON.parse(fetchMock.mock.calls[0][1].body)
    expect(reqBody.text).toBe('存一个华为云 SWR 凭据')
    expect(reqBody.messages.length).toBe(1) // history 不含本轮

    const text = bodyText()
    expect(text).toContain('存一个华为云 SWR 凭据') // 用户消息
    expect(text).toContain('正在解析') // status 行
    expect(text).toContain('已解析出条目') // assistant 回复（Markdown 渲染）
    expect(text).toContain('华为云 SWR') // 草稿卡标题
    expect(text).toContain('ci-bot') // 非敏感字段明文
    expect(text).toContain('🔒') // 敏感字段遮蔽标记
    expect(text).not.toContain('super-secret-123') // 敏感明文绝不上屏
    expect(findButton('确认入库')).toBeTruthy()
    // loading 复位：发送按钮不再禁用、输入区可用
    expect((findButton('发送（Enter）') as HTMLButtonElement).disabled).toBe(false)
    expect(ta.disabled).toBe(false)
    w.unmount()
  })

  it('草稿确认入库：POST /entries、广播条目变更、成功标记', async () => {
    stubSSE([
      { type: 'draft', draft: draftEntry, draft_kind: 'create' },
      { type: 'done', text: '' },
    ])
    const w = mount(ChatDrawer, { global: { plugins: [ElementPlus] } })
    await openDrawer(w)
    const ta = document.querySelector('textarea') as HTMLTextAreaElement
    ta.value = '存凭据'
    ta.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    findButton('发送（Enter）')!.click()
    await flushPromises()
    findButton('确认入库')!.click()
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/entries', expect.objectContaining({
      method: 'POST',
      body: expect.stringContaining('"title":"华为云 SWR"'),
    }))
    expect(notifyMock).toHaveBeenCalled() // 通知条目列表页刷新
    expect(bodyText()).toContain('已入库')
    w.unmount()
  })

  it('清空对话：重置为欢迎语', async () => {
    stubSSE([{ type: 'reply_delta', text: '收到' }, { type: 'done', text: '' }])
    const w = mount(ChatDrawer, { global: { plugins: [ElementPlus] } })
    await openDrawer(w)
    const ta = document.querySelector('textarea') as HTMLTextAreaElement
    ta.value = '临时内容'
    ta.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    findButton('发送（Enter）')!.click()
    await flushPromises()
    expect(bodyText()).toContain('临时内容')
    findButton('清空对话')!.click()
    await nextTick()
    expect(bodyText()).not.toContain('临时内容')
    expect(bodyText()).not.toContain('收到')
    expect(bodyText()).toContain('大白话') // 回到欢迎语
    w.unmount()
  })

  it('SSE 出错事件：错误文案拼入回复且状态复位', async () => {
    stubSSE([{ type: 'error', text: 'LLM 服务商超时' }])
    const w = mount(ChatDrawer, { global: { plugins: [ElementPlus] } })
    await openDrawer(w)
    const ta = document.querySelector('textarea') as HTMLTextAreaElement
    ta.value = 'hi'
    ta.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    findButton('发送（Enter）')!.click()
    await flushPromises()
    expect(bodyText()).toContain('❌ LLM 服务商超时')
    expect((findButton('发送（Enter）') as HTMLButtonElement).disabled).toBe(false)
    w.unmount()
  })
})
