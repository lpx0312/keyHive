import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { api } from './api'

// mock fetch：记录请求并返回预设响应
function mockFetch(status: number, body: any) {
  const calls: { url: string; init: RequestInit }[] = []
  const impl = vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    return new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })
  })
  vi.stubGlobal('fetch', impl)
  return { calls }
}

describe('api()', () => {
  beforeEach(() => {
    // jsdom 的 location 不可直接赋值：用 replace 技巧固定在 /entries
    window.history.replaceState({}, '', '/entries')
  })
  afterEach(() => vi.unstubAllGlobals())

  it('拼接 /api/v1 前缀并返回 JSON', async () => {
    const { calls } = mockFetch(200, { ok: true, list: [1, 2] })
    const out = await api('/entries')
    expect(out).toEqual({ ok: true, list: [1, 2] })
    expect(calls[0].url).toBe('/api/v1/entries')
    expect(calls[0].init.headers).toMatchObject({ 'Content-Type': 'application/json' })
    expect(calls[0].init.credentials).toBe('same-origin')
  })

  it('非 2xx 抛出后端 error 消息', async () => {
    mockFetch(400, { error: '标题不能为空' })
    await expect(api('/entries', { method: 'POST', body: '{}' })).rejects.toThrow('标题不能为空')
  })

  it('非 2xx 且无 error 字段时抛状态码消息', async () => {
    mockFetch(502, {})
    await expect(api('/x')).rejects.toThrow('请求失败 (502)')
  })

  it('响应体非 JSON 时容错为空对象并抛状态码消息', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response('gateway crashed', { status: 502 })))
    await expect(api('/x')).rejects.toThrow('请求失败 (502)')
  })

  it('401 时抛"未登录"（jsdom 下导航为 no-op，只验证分支进入）', async () => {
    mockFetch(401, { error: '未登录' })
    await expect(api('/entries')).rejects.toThrow('未登录')
  })

  it('401 且已在 /login 时不跳转（避免循环）', async () => {
    window.history.replaceState({}, '', '/login')
    mockFetch(401, { error: '未登录' })
    const origHref = Object.getOwnPropertyDescriptor(window.Location.prototype, 'href')
    Object.defineProperty(window.Location.prototype, 'href', {
      configurable: true,
      set() { throw new Error('不应发生跳转') },
      get() { return 'http://localhost/login' },
    })
    try {
      await expect(api('/auth/login', { method: 'POST' })).rejects.toThrow()
    } finally {
      origHref && Object.defineProperty(window.Location.prototype, 'href', origHref)
    }
  })

  it('自定义 opts 透传（method/body）', async () => {
    const { calls } = mockFetch(201, { id: 9 })
    await api('/entries', { method: 'POST', body: '{"title":"T"}' })
    expect(calls[0].init.method).toBe('POST')
    expect(calls[0].init.body).toBe('{"title":"T"}')
  })
})
