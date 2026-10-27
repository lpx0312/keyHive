import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import Templates from './Templates.vue'
import { mockTemplates } from '../test/fixtures'

const apiMock = vi.fn()
vi.mock('../api', () => ({ api: (...a: any[]) => apiMock(...a) }))
vi.mock('element-plus', async () => {
  const real = await vi.importActual<any>('element-plus')
  return { ...real, ElMessageBox: { ...real.ElMessageBox, confirm: vi.fn().mockResolvedValue('') } }
})

describe('Templates.vue', () => {
  beforeEach(() => {
    apiMock.mockReset()
    apiMock.mockResolvedValue(mockTemplates)
  })

  it('挂载拉取模板并按分组渲染卡片', async () => {
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/templates')
    expect(w.text()).toContain('录入模板')
    expect(w.text()).toContain('容器镜像')
    expect(w.text()).toContain('Docker 镜像仓库')
    expect(w.text()).toContain('我的模板')
  })

  it('内置/自建卡片区分：自建带徽章与删除按钮，内置只有编辑', async () => {
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    const cards = w.findAll('article.tpl-card')
    expect(cards.length).toBe(2)
    // 内置模板：无「自建」徽章、无删除按钮
    expect(cards[0].text()).not.toContain('自建')
    expect(cards[0].findAll('button').map(b => b.text())).not.toContain('删除')
    // 自建模板：有徽章 + 删除按钮
    expect(cards[1].text()).toContain('自建')
    expect(cards[1].findAll('button').map(b => b.text())).toContain('删除')
  })

  it('字段骨架完整渲染：字段名/说明/敏感锁标与非 text 类型标签', async () => {
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('url')
    expect(w.text()).toContain('仓库地址')
    expect(w.text()).toContain('网址') // type=url 的小标签
    expect(w.find('.fld-lock').exists()).toBe(true) // 敏感锁图标
  })

  it('空字段模板不崩溃（fields: []）', async () => {
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('我的模板') // fields 为空的自建模板所在分组仍渲染
  })

  it('搜索按模板名/字段名过滤，无匹配显示空态', async () => {
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    const input = w.find('input[placeholder="搜索模板 / 字段"]')
    await input.setValue('docker')
    await flushPromises()
    expect(w.text()).toContain('Docker 镜像仓库')
    expect(w.text()).not.toContain('我的模板')
    // 按字段名搜索也命中
    await input.setValue('password')
    expect(w.text()).toContain('Docker 镜像仓库')
    // 无匹配
    await input.setValue('不存在的关键字xyz')
    expect(w.text()).toContain('没有匹配的模板')
  })

  it('空列表渲染空态', async () => {
    apiMock.mockResolvedValue([])
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('暂无模板')
  })

  it('新增模板：校验链（空名/无字段/缺说明）都不发请求', async () => {
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.find('.page-actions button').trigger('click') // 新增模板
    await flushPromises()
    apiMock.mockClear()
    const dlg = w.find('.el-dialog')
    expect(dlg.exists()).toBe(true)
    // 打开时自带一个空字段行
    expect(w.findAll('.frow').length).toBe(1)
    // 空模板名 → 拦截
    await w.findAll('button').filter(b => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    expect(apiMock).not.toHaveBeenCalled()
  })

  it('新增模板：填齐后 POST 且刷新列表', async () => {
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.find('.page-actions button').trigger('click')
    await flushPromises()
    // 模板名（弹窗内第一个可见输入）
    const nameInput = w.find('input[placeholder="如：Zabbix 监控"]')
    await nameInput.setValue('测试模板')
    const keyInput = w.find('input[placeholder="英文，如 access_key_id"]')
    await keyInput.setValue('console_url')
    const descInput = w.find('input[placeholder="如：控制台登录地址"]')
    await descInput.setValue('控制台地址')
    apiMock.mockClear()
    apiMock.mockResolvedValue(mockTemplates)
    await w.findAll('button').filter(b => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    const post = apiMock.mock.calls.find(c => c[1] && c[1].method === 'POST')
    expect(post).toBeTruthy()
    expect(post![0]).toBe('/templates')
    const body = JSON.parse(post![1].body)
    expect(body.name).toBe('测试模板')
    expect(body.fields).toEqual([{ key: 'console_url', description: '控制台地址', type: 'text', is_secret: false, value: '' }])
  })

  it('编辑模板：openDlg 深拷贝回填，保存走 PUT', async () => {
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    const editBtn = w.findAll('button').filter(b => b.text() === '编辑')[0]
    await editBtn.trigger('click')
    await flushPromises()
    const nameInput = w.find('input[placeholder="如：Zabbix 监控"]')
    expect((nameInput.element as HTMLInputElement).value).toBe('Docker 镜像仓库')
    apiMock.mockClear()
    apiMock.mockResolvedValue(mockTemplates)
    await w.findAll('button').filter(b => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    const put = apiMock.mock.calls.find(c => c[1] && c[1].method === 'PUT')
    expect(put![0]).toBe('/templates/1')
  })

  it('添加/删除字段行', async () => {
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.find('.page-actions button').trigger('click')
    await flushPromises()
    expect(w.findAll('.frow').length).toBe(1)
    await w.findAll('button').filter(b => b.text() === '添加字段')[0].trigger('click')
    expect(w.findAll('.frow').length).toBe(2)
    // 删除第一行（圆形 ✕ 按钮带 tooltip，直接找 .frow-del）
    await w.find('.frow-del').trigger('click')
    expect(w.findAll('.frow').length).toBe(1)
  })

  it('删除模板：确认后 DELETE 并刷新', async () => {
    const w = mount(Templates, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockResolvedValue(mockTemplates)
    const delBtn = w.findAll('button').filter(b => b.text() === '删除')[0]
    await delBtn.trigger('click')
    await flushPromises()
    const del = apiMock.mock.calls.find(c => c[1] && c[1].method === 'DELETE')
    expect(del).toBeTruthy()
    expect(del![0]).toBe('/templates/2') // 自建模板 id=2
  })
})
