import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import Tokens from './Tokens.vue'
import { mockTokens } from '../test/fixtures'

const apiMock = vi.fn()
const copyTextMock = vi.fn().mockResolvedValue(true)
vi.mock('../api', () => ({ api: (...a: any[]) => apiMock(...a) }))
vi.mock('../ui', () => ({ copyText: (...a: any[]) => copyTextMock(...a) }))
vi.mock('element-plus', async () => {
  const real = await vi.importActual<any>('element-plus')
  return { ...real, ElMessageBox: { ...real.ElMessageBox, confirm: vi.fn().mockResolvedValue('') } }
})

describe('Tokens.vue', () => {
  beforeEach(() => {
    apiMock.mockReset()
    apiMock.mockResolvedValue(mockTokens)
    copyTextMock.mockReset()
    copyTextMock.mockResolvedValue(true)
  })

  it('挂载拉取令牌并渲染名称/权限/状态', async () => {
    const w = mount(Tokens, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/tokens')
    expect(w.text()).toContain('AI 访问令牌')
    expect(w.text()).toContain('zcode-mcp')
    expect(w.text()).toContain('reveal')
    expect(w.text()).toContain('有效')
  })

  it('已吊销令牌不显示吊销按钮（边界）', async () => {
    apiMock.mockResolvedValue([{ ...mockTokens[0], revoked_at: '2026-10-26T00:00:00Z' }])
    const w = mount(Tokens, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('已吊销')
    expect(w.findAll('button').map(b => b.text())).not.toContain('吊销')
  })

  it('空列表不崩溃', async () => {
    apiMock.mockResolvedValue([])
    const w = mount(Tokens, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('AI 访问令牌')
  })

  it('新建令牌：名称为空拦截不发请求（边界）', async () => {
    const w = mount(Tokens, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.find('.page-actions button').trigger('click')
    await flushPromises()
    apiMock.mockClear()
    // 名称留空直接点创建
    const createBtn = w.findAll('.el-dialog button').filter(b => b.text() === '创建')
    await createBtn[createBtn.length - 1].trigger('click')
    await flushPromises()
    expect(apiMock).not.toHaveBeenCalled()
  })

  it('新建令牌：scopes 组装（read 必带 + 勾选项）', async () => {
    const w = mount(Tokens, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.find('.page-actions button').trigger('click')
    await flushPromises()
    await w.find('input[placeholder="如 cursor-mcp、claude-skill"]').setValue('test-token')
    apiMock.mockClear()
    // POST 返回新令牌（含明文），GET 列表仍返回数组
    apiMock.mockImplementation((_path: string, opts?: any) =>
      opts?.method === 'POST'
        ? Promise.resolve({ ...mockTokens[0], token: 'kh_tok_plain' })
        : Promise.resolve(mockTokens))
    const createBtns = w.findAll('.el-dialog button').filter(b => b.text() === '创建')
    await createBtns[createBtns.length - 1].trigger('click')
    await flushPromises()
    const post = apiMock.mock.calls.find(c => c[1] && c[1].method === 'POST')
    expect(post).toBeTruthy()
    // 默认勾选 search、未勾 reveal
    expect(JSON.parse(post![1].body)).toEqual({ name: 'test-token', scopes: ['read', 'search'] })
    // 创建成功后弹出仅此一次显示的明文框
    expect(w.text()).toContain('仅此一次显示')
  })

  it('吊销：确认后 DELETE /tokens/:id 并刷新', async () => {
    const w = mount(Tokens, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockResolvedValue(mockTokens)
    const revokeBtn = w.findAll('button').filter(b => b.text() === '吊销')
    expect(revokeBtn.length).toBe(1)
    await revokeBtn[0].trigger('click')
    await flushPromises()
    const del = apiMock.mock.calls.find(c => c[1] && c[1].method === 'DELETE')
    expect(del).toBeTruthy()
    expect(del![0]).toBe('/tokens/1')
  })

  it('复制成功提示（copyText true 分支）', async () => {
    const w = mount(Tokens, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.find('.page-actions button').trigger('click')
    await flushPromises()
    await w.find('input[placeholder="如 cursor-mcp、claude-skill"]').setValue('t')
    apiMock.mockImplementation((_p: string, opts?: any) =>
      opts?.method === 'POST'
        ? Promise.resolve({ ...mockTokens[0], token: 'kh_tok_plain' })
        : Promise.resolve(mockTokens))
    const createBtns = w.findAll('.el-dialog button').filter(b => b.text() === '创建')
    await createBtns[createBtns.length - 1].trigger('click')
    await flushPromises()
    // 「令牌已创建」弹窗中的复制按钮
    const copyBtns = w.findAll('button').filter(b => b.text() === '复制')
    expect(copyBtns.length).toBeGreaterThan(0)
    apiMock.mockClear()
    await copyBtns[0].trigger('click')
    await flushPromises()
    // copyText 已 mock true，不触发额外请求、不崩溃
    expect(w.text()).toContain('仅此一次显示')
  })

  it('复制失败降级提示（copyText false 分支，HTTP 局域网降级场景）', async () => {
    copyTextMock.mockResolvedValue(false)
    const w = mount(Tokens, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.find('.page-actions button').trigger('click')
    await flushPromises()
    await w.find('input[placeholder="如 cursor-mcp、claude-skill"]').setValue('t')
    apiMock.mockImplementation((_p: string, opts?: any) =>
      opts?.method === 'POST'
        ? Promise.resolve({ ...mockTokens[0], token: 'kh_tok_plain' })
        : Promise.resolve(mockTokens))
    const createBtns = w.findAll('.el-dialog button').filter(b => b.text() === '创建')
    await createBtns[createBtns.length - 1].trigger('click')
    await flushPromises()
    const copyBtns = w.findAll('button').filter(b => b.text() === '复制')
    await copyBtns[0].trigger('click')
    await flushPromises()
    // 明文仍显示在输入框中供手动复制，页面不崩
    expect(w.find('.el-dialog input').element as HTMLInputElement).toBeTruthy()
  })

  it('新建令牌：勾选 reveal 后 scopes 含 reveal（权限边界）', async () => {
    const w = mount(Tokens, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.find('.page-actions button').trigger('click')
    await flushPromises()
    await w.find('input[placeholder="如 cursor-mcp、claude-skill"]').setValue('full-token')
    // 勾选 reveal（新建弹窗内最后一个 checkbox；read 是 disabled 的第一个）
    const boxes = w.findAll('.el-dialog input[type="checkbox"]')
    const revealInput = boxes[boxes.length - 1]
    await revealInput.setValue(true)
    apiMock.mockImplementation((_p: string, opts?: any) =>
      opts?.method === 'POST'
        ? Promise.resolve({ ...mockTokens[0], token: 'kh_tok_plain' })
        : Promise.resolve(mockTokens))
    const createBtns = w.findAll('.el-dialog button').filter(b => b.text() === '创建')
    await createBtns[createBtns.length - 1].trigger('click')
    await flushPromises()
    const post = apiMock.mock.calls.find(c => c[1] && c[1].method === 'POST')
    expect(JSON.parse(post![1].body)).toEqual({ name: 'full-token', scopes: ['read', 'search', 'reveal'] })
  })
})
