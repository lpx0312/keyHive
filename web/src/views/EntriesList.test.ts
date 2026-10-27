import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import EntriesList from './EntriesList.vue'
import { mockEntries } from '../test/fixtures'

const apiMock = vi.fn()
const pushMock = vi.fn()
vi.mock('../api', () => ({ api: (...a: any[]) => apiMock(...a) }))
vi.mock('../store', () => ({ me: { id: 1, username: 'admin', isAdmin: true }, ENTRIES_CHANGED_EVENT: 'kh-entries-changed' }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: pushMock }) }))
const { mobileFlag } = vi.hoisted(() => ({ mobileFlag: { value: false } }))
vi.mock('../ui', async () => {
  const { ref } = await import('vue')
  return { useIsMobile: () => ref(mobileFlag.value) } // 必须真 ref：普通对象不参与模板自动解包
})
vi.mock('element-plus', async () => {
  const real = await vi.importActual<any>('element-plus')
  return { ...real, ElMessageBox: { ...real.ElMessageBox, confirm: vi.fn().mockResolvedValue('') } }
})

function mockDefaults() {
  apiMock.mockImplementation((path: string) => {
    if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
    if (path === '/categories') return Promise.resolve(['docker_registry', 'web_account'])
    if (path === '/settings/stale-days') return Promise.resolve({ stale_days: 90 })
    if (path === '/export') return Promise.resolve(mockEntries)
    return Promise.resolve({})
  })
}

describe('EntriesList.vue', () => {
  beforeEach(() => {
    apiMock.mockReset()
    mockDefaults()
    pushMock.mockReset()
    mobileFlag.value = false
  })

  it('挂载并行拉取条目/分类/轮换阈值并渲染表格', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/entries?')
    expect(apiMock).toHaveBeenCalledWith('/categories')
    expect(w.text()).toContain('密钥条目')
    expect(w.text()).toContain('生产ACR')
    expect(w.text()).toContain('内网Git')
    expect(w.text()).toContain('共 2 条')
  })

  it('URL 列提取：type=url 的非敏感字段渲染为链接', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.html()).toContain('https://git.corp')
  })

  it('URL 提取边界：仅有敏感 url 字段或遮蔽值时不渲染链接', async () => {
    apiMock.mockImplementation((path: string) => {
      if (String(path).startsWith('/entries')) return Promise.resolve([
        { ...mockEntries[1], id: 9, fields: [{ key: 'url', description: '地址', type: 'url', is_secret: true, value: 'https://secret.example' }] },
        { ...mockEntries[1], id: 10, title: '遮蔽URL', fields: [{ key: 'url', description: '地址', type: 'url', is_secret: false, value: '***' }] },
      ])
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 90 })
    })
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.html()).not.toContain('https://secret.example')
    expect(w.html()).not.toContain('>***<')
  })

  it('空结果 null 容错（v1.3.6 回归：后端可能返回 null）', async () => {
    apiMock.mockImplementation((path: string) => {
      if (String(path).startsWith('/entries')) return Promise.resolve(null)
      if (path === '/categories') return Promise.resolve(null)
      return Promise.resolve({ stale_days: 90 })
    })
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('密钥条目') // 页面不崩
  })

  it('标签与 AI 可见徽章渲染', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('生产') // mockEntries[0] 的标签
    expect(w.text()).toContain('内网')
    expect(w.text()).toContain('AI 不可见') // mockEntries[1]
  })

  it('轮换提醒：超过阈值天数的条目带警告（边界）', async () => {
    apiMock.mockImplementation((path: string) => {
      if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 3 }) // 阈值 3 天，两条目都超期
    })
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    // 桌面表格：超期条目的更新时间带 ⚠️（完整文案在 tooltip 中）
    expect(w.text()).toContain('⚠️')
  })

  it('搜索带 q 参数并重置页码', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockImplementation((path: string) => {
      if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 90 })
    })
    const search = w.find('input[placeholder="搜索标题 / 说明 / 分类"]')
    await search.setValue('ACR')
    await flushPromises()
    expect(String(apiMock.mock.calls[0][0])).toContain('q=ACR')
  })

  it('删除：确认后 DELETE /entries/:id 并刷新', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockImplementation((path: string) => {
      if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 90 })
    })
    const delBtns = w.findAll('button').filter(b => b.text() === '删除')
    expect(delBtns.length).toBeGreaterThan(0)
    await delBtns[0].trigger('click')
    await flushPromises()
    const del = apiMock.mock.calls.find(c => c[1] && c[1].method === 'DELETE')
    expect(del).toBeTruthy()
    expect(del![0]).toBe('/entries/1')
  })

  it('复制：跳转 /entries/new?from=:id', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    const copyBtns = w.findAll('button').filter(b => b.text() === '复制')
    await copyBtns[0].trigger('click')
    await flushPromises()
    expect(pushMock).toHaveBeenCalledWith('/entries/new?from=1')
  })

  it('ENTRIES_CHANGED 事件触发自动刷新（AI 助手入库联动）', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockImplementation((path: string) => {
      if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 90 })
    })
    window.dispatchEvent(new Event('kh-entries-changed'))
    await flushPromises()
    expect(apiMock).toHaveBeenCalled()
    w.unmount()
  })

  it('导出遮蔽版：敏感值替换为 ***（Blob 下载链路）', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    // 打开导出弹窗，默认遮蔽版；打桩 a.click 触发下载
    const clickSpy = vi.fn()
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(clickSpy)
    const expBtn = w.findAll('button').filter(b => b.text() === '导出')[0]
    await expBtn.trigger('click')
    await flushPromises()
    const dlBtn = w.findAll('.el-dialog button').filter(b => b.text() === '下载 JSON')
    await dlBtn[dlBtn.length - 1].trigger('click')
    await flushPromises()
    expect(clickSpy).toHaveBeenCalled()
    vi.mocked(HTMLAnchorElement.prototype.click).mockRestore?.()
  })

  it('加载失败提示错误且页面不崩（catch 分支）', async () => {
    apiMock.mockRejectedValue(new Error('会话过期'))
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('密钥条目')
  })

  it('批量删除：勾选两行后逐条 DELETE 并汇总（边界：含失败项）', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    // 勾选两行（selection 列 checkbox：对 input setValue 触发 change，jsdom 下 label click 不联动）
    const inputs = w.findAll('.el-table__row input[type="checkbox"]')
    expect(inputs.length).toBe(2)
    await inputs[0].setValue(true)
    await inputs[1].setValue(true)
    await flushPromises()
    const batchBtn = w.findAll('button').filter(b => b.text().includes('删除选中'))
    expect(batchBtn.length).toBe(1)
    // 第一条删除成功、第二条失败
    apiMock.mockClear()
    apiMock.mockImplementation((path: string, opts?: any) => {
      if (opts?.method === 'DELETE' && path === '/entries/1') return Promise.resolve({ ok: true })
      if (opts?.method === 'DELETE') return Promise.reject(new Error('被引用'))
      if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 90 })
    })
    await batchBtn[0].trigger('click')
    await flushPromises()
    const dels = apiMock.mock.calls.filter(c => c[1] && c[1].method === 'DELETE')
    expect(dels.length).toBe(2)
  })

  it('导入弹窗：来源格式选择 + 默认提示文案（边界）', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '导入')[0].trigger('click')
    await flushPromises()
    // 下拉选项在 teleport 懒渲染中，断言弹窗主体与默认（非 keyhive）提示
    expect(w.text()).toContain('导入')
    expect(w.text()).toContain('来源格式')
    expect(w.text()).toContain('导入的条目归入 web_account 分类')
    expect(w.find('input[type="file"]').exists()).toBe(true)
  })

  it('导入：选文件自动预览，确认后 POST dry_run=false', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '导入')[0].trigger('click')
    await flushPromises()
    // jsdom 下手动挂 File 到 input.files 并触发 change（FileReader 异步读完自动预览）
    const file = new File(['name,username,password\nGit,alice,pw1'], 'bw.csv', { type: 'text/csv' })
    const fileInput = w.find('input[type="file"]')
    Object.defineProperty(fileInput.element, 'files', { value: [file] })
    apiMock.mockClear()
    apiMock.mockImplementation((path: string, opts?: any) => {
      if (path === '/import') {
        const body = JSON.parse(opts?.body || '{}')
        if (body.dry_run) return Promise.resolve({ count: 1, preview: [{ title: 'Git', username: 'alice' }] })
        return Promise.resolve({ ok: 1, fail: [] })
      }
      if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 90 })
    })
    await fileInput.trigger('change')
    // FileReader onload 走宏任务队列，waitFor 等预览请求发出
    await vi.waitFor(() => expect(apiMock.mock.calls.some(c => c[0] === '/import')).toBe(true))
    const prev = apiMock.mock.calls.find(c => c[0] === '/import')
    expect(prev).toBeTruthy()
    expect(JSON.parse(prev![1].body).dry_run).toBe(true)
    expect(w.text()).toContain('共 1 条')
    expect(w.text()).toContain('Git')
    // 确认导入
    await w.findAll('.el-dialog button').filter(b => b.text() === '确认导入')[0].trigger('click')
    await flushPromises()
    const real = apiMock.mock.calls.filter(c => c[0] === '/import').pop()
    expect(JSON.parse(real![1].body).dry_run).toBe(false)
  })

  it('导出失败：提示错误不崩溃（catch 分支）', async () => {
    apiMock.mockImplementation((path: string) => {
      if (path === '/export') return Promise.reject(new Error('主密钥不匹配'))
      if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 90 })
    })
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '导出')[0].trigger('click')
    await flushPromises()
    const dlBtn = w.findAll('.el-dialog button').filter(b => b.text() === '下载 JSON')
    await dlBtn[dlBtn.length - 1].trigger('click')
    await flushPromises()
    expect(w.text()).toContain('密钥条目') // 页面不崩
  })

  it('确认导入失败：提示错误不崩溃（catch 分支）', async () => {
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '导入')[0].trigger('click')
    await flushPromises()
    const file = new File(['name\nGit'], 'bw.csv', { type: 'text/csv' })
    const fileInput = w.find('input[type="file"]')
    Object.defineProperty(fileInput.element, 'files', { value: [file] })
    apiMock.mockClear()
    apiMock.mockImplementation((path: string, opts?: any) => {
      if (path === '/import') {
        const body = JSON.parse(opts?.body || '{}')
        if (body.dry_run) return Promise.resolve({ count: 1, preview: [{ title: 'Git', username: '' }] })
        return Promise.reject(new Error('写入失败'))
      }
      if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 90 })
    })
    await fileInput.trigger('change')
    await vi.waitFor(() => expect(w.text()).toContain('共 1 条'))
    await w.findAll('.el-dialog button').filter(b => b.text() === '确认导入')[0].trigger('click')
    await flushPromises()
    expect(w.text()).toContain('密钥条目') // 页面不崩
  })

  it('移动端：卡片列表渲染 + 卡片操作（编辑/复制/删除）', async () => {
    mobileFlag.value = true
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.find('.cards').exists()).toBe(true)
    expect(w.findAll('.card').length).toBe(2)
    // 卡片 URL 行与说明
    expect(w.text()).toContain('https://git.corp')
    expect(w.text()).toContain('CI 拉镜像用')
    // 卡片「编辑」→ 跳编辑页；「复制」→ ?from
    const acts = w.findAll('.card button')
    await acts.filter(b => b.text() === '编辑')[0].trigger('click')
    expect(pushMock).toHaveBeenCalledWith('/entries/1/edit')
    await acts.filter(b => b.text() === '复制')[0].trigger('click')
    expect(pushMock).toHaveBeenCalledWith('/entries/new?from=1')
    // 卡片删除
    apiMock.mockClear()
    apiMock.mockImplementation((path: string) => {
      if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 90 })
    })
    await acts.filter(b => b.text() === '删除')[0].trigger('click')
    await flushPromises()
    expect(apiMock.mock.calls.some(c => c[1]?.method === 'DELETE')).toBe(true)
  })

  it('URL 提取第二优先级：按常见 key 名（host）匹配非敏感字段', async () => {
    apiMock.mockImplementation((path: string) => {
      if (String(path).startsWith('/entries')) return Promise.resolve([
        { ...mockEntries[1], id: 7, title: '按key提取', fields: [{ key: 'host', description: '主机', type: 'text', is_secret: false, value: 'db.internal:3306' }] },
      ])
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ stale_days: 90 })
    })
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.html()).toContain('https://db.internal:3306') // 无协议时补 https://
  })

  it('筛选交互：分类下拉切换带参数重拉；标签筛选本地过滤', async () => {
    document.body.innerHTML = '' // mount 前清残留（mount 后清会破坏已建 popper）
    const w = mount(EntriesList, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    // 分类下拉（page-actions 里第一个 el-select）选 docker_registry
    const sels = w.findAll('.page-actions .el-select')
    await sels[0].find('.el-select__wrapper').trigger('click')
    await flushPromises()
    const opt = Array.from(document.querySelectorAll('.el-select-dropdown__item'))
      .find(el => (el.textContent || '').trim() === 'docker_registry')
    expect(opt).toBeTruthy()
    apiMock.mockClear()
    apiMock.mockImplementation((path: string) => {
      if (String(path).startsWith('/entries')) return Promise.resolve(mockEntries)
      if (path === '/categories') return Promise.resolve(['docker_registry', 'web_account'])
      return Promise.resolve({ stale_days: 90 })
    })
    opt!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    const call = apiMock.mock.calls.find(c => String(c[0]).startsWith('/entries?'))
    expect(String(call![0])).toContain('category=docker_registry')
  })

  it('新建条目按钮跳转 /entries/new', async () => {
    const routerStub = { install: (app: any) => { app.config.globalProperties.$router = { push: pushMock } } }
    const w = mount(EntriesList, { global: { plugins: [ElementPlus, routerStub] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '新建条目')[0].trigger('click')
    expect(pushMock).toHaveBeenCalledWith('/entries/new')
  })
})
