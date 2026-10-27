import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import Audit from './Audit.vue'
import { mockAuditLogs } from '../test/fixtures'

const apiMock = vi.fn()
vi.mock('../api', () => ({ api: (...a: any[]) => apiMock(...a) }))
vi.mock('../ui', async () => {
  const { ref } = await import('vue')
  return { useIsMobile: () => ref(false) }
})

function pageOfLogs(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    ...mockAuditLogs[0],
    id: i + 1,
    detail: `{"seq":${i}}`,
  }))
}

describe('Audit.vue', () => {
  beforeEach(() => {
    apiMock.mockReset()
    apiMock.mockResolvedValue({ logs: mockAuditLogs, total: mockAuditLogs.length })
    document.body.innerHTML = '' // 清理 teleport 残留
  })

  it('动作筛选：切换 action 回第 1 页并带 action 参数', async () => {
    apiMock.mockResolvedValue({ logs: pageOfLogs(15), total: 40 })
    const w = mount(Audit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    // 展开动作下拉选「登录」（options teleport 到 body）
    await w.find('.page-actions .el-select').trigger('click')
    await flushPromises()
    const opt = Array.from(document.querySelectorAll('.el-select-dropdown__item'))
      .find(el => (el.textContent || '').trim() === '登录')
    expect(opt).toBeTruthy()
    apiMock.mockClear()
    apiMock.mockResolvedValue({ logs: mockAuditLogs, total: 1 })
    opt!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    const call = apiMock.mock.calls.find(c => String(c[0]).startsWith('/audit?'))
    expect(String(call![0])).toContain('action=login')
    expect(String(call![0])).toContain('offset=0')
  })

  it('挂载带 limit/offset/with_total 请求并渲染日志', async () => {
    const w = mount(Audit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    const call = apiMock.mock.calls.find(c => String(c[0]).startsWith('/audit?'))
    expect(call).toBeTruthy()
    const q = String(call![0])
    expect(q).toContain('limit=15')
    expect(q).toContain('offset=0')
    expect(q).toContain('with_total=1')
    expect(w.text()).toContain('审计日志')
    expect(w.text()).toContain('admin')
    expect(w.text()).toContain('新建条目') // label 映射
    expect(w.text()).toContain('AI 取敏感值') // field_reveal
    expect(w.text()).toContain('登录失败') // login_failed
  })

  it('未知动作回退显示原始值（边界）', async () => {
    apiMock.mockResolvedValue({ logs: [{ ...mockAuditLogs[0], action: 'custom_action' }], total: 1 })
    const w = mount(Audit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('custom_action')
  })

  it('actor_name 缺失回退 actor_id（边界）', async () => {
    apiMock.mockResolvedValue({ logs: [{ ...mockAuditLogs[0], actor_name: '', actor_id: 42 }], total: 1 })
    const w = mount(Audit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('42')
  })

  it('空日志列表不崩溃', async () => {
    apiMock.mockResolvedValue({ logs: [], total: 0 })
    const w = mount(Audit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('审计日志')
  })

  it('翻页参数正确：第 2 页 offset=pageSize', async () => {
    apiMock.mockResolvedValue({ logs: pageOfLogs(15), total: 40 })
    const w = mount(Audit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockResolvedValue({ logs: pageOfLogs(15), total: 40 })
    // 点第 2 页
    const pagerBtns = w.findAll('.el-pager li')
    expect(pagerBtns.length).toBeGreaterThan(0)
    await pagerBtns.filter(li => li.text() === '2')[0].trigger('click')
    await flushPromises()
    const call = apiMock.mock.calls.find(c => String(c[0]).startsWith('/audit?'))
    expect(String(call![0])).toContain('offset=15')
  })

  it('筛选后总页数变少时钳到最后一页重拉（边界）', async () => {
    // 第一页 15 条、total=40；翻到第 3 页后筛选动作使 total=3（只剩 1 页）
    apiMock.mockResolvedValue({ logs: pageOfLogs(15), total: 40 })
    const w = mount(Audit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockResolvedValue({ logs: pageOfLogs(3), total: 3 })
    const pagerBtns = w.findAll('.el-pager li')
    await pagerBtns.filter(li => li.text() === '3')[0].trigger('click')
    await flushPromises()
    // 触发钳制重拉：最终请求 offset=0（回到第 1 页）
    const calls = apiMock.mock.calls.filter(c => String(c[0]).startsWith('/audit?'))
    const last = String(calls[calls.length - 1][0])
    expect(last).toContain('offset=0')
  })
})
