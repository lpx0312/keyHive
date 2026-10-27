import { describe, it, expect, vi, beforeEach } from 'vitest'
import { me, loadMe, ENTRIES_CHANGED_EVENT, notifyEntriesChanged, FAB_HIDDEN_EVENT, setFabHidden } from './store'
import { api } from './api'

vi.mock('./api', () => ({
  api: vi.fn(),
}))

describe('store：登录态', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
    me.id = 0
    me.username = ''
    me.isAdmin = false
    me.loaded = false
    localStorage.clear()
  })

  it('loadMe 成功填充 me', async () => {
    vi.mocked(api).mockResolvedValue({ id: 3, username: 'alice', is_admin: true })
    await loadMe()
    expect(me.id).toBe(3)
    expect(me.username).toBe('alice')
    expect(me.isAdmin).toBe(true)
    expect(me.loaded).toBe(true)
  })

  it('is_admin 缺省/假值时 isAdmin=false', async () => {
    vi.mocked(api).mockResolvedValue({ id: 4, username: 'bob', is_admin: false })
    await loadMe()
    expect(me.isAdmin).toBe(false)
  })

  it('loadMe 失败不抛出（api 内部已处理 401）', async () => {
    vi.mocked(api).mockRejectedValue(new Error('网络错误'))
    await expect(loadMe()).resolves.toBeUndefined()
    expect(me.loaded).toBe(false)
  })
})

describe('store：事件与悬浮球', () => {
  it('notifyEntriesChanged 广播 keyhive:entries-changed', () => {
    const handler = vi.fn()
    window.addEventListener(ENTRIES_CHANGED_EVENT, handler)
    notifyEntriesChanged()
    expect(handler).toHaveBeenCalledTimes(1)
    window.removeEventListener(ENTRIES_CHANGED_EVENT, handler)
  })

  it('setFabHidden 写 localStorage 并广播事件', () => {
    const handler = vi.fn()
    window.addEventListener(FAB_HIDDEN_EVENT, handler)
    setFabHidden(true)
    expect(localStorage.getItem('kh-fab-hidden')).toBe('1')
    expect(handler).toHaveBeenCalledTimes(1)
    setFabHidden(false)
    expect(localStorage.getItem('kh-fab-hidden')).toBe('0')
    expect(handler).toHaveBeenCalledTimes(2)
    window.removeEventListener(FAB_HIDDEN_EVENT, handler)
  })
})
