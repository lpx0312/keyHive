import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import EntryEdit from './EntryEdit.vue'
import { mockEntries, mockTemplates } from '../test/fixtures'

const apiMock = vi.fn()
const pushMock = vi.fn()
// 路由状态按测试切换（新建 / 复制新建 / 编辑）
const routeMock: { path: string; params: Record<string, string>; query: Record<string, string> } = {
  path: '/entries/new', params: {}, query: {},
}
vi.mock('../api', () => ({ api: (...a: any[]) => apiMock(...a) }))
vi.mock('vue-router', () => ({
  useRoute: () => routeMock,
  useRouter: () => ({ push: pushMock }),
}))
const { mobileFlag } = vi.hoisted(() => ({ mobileFlag: { value: false } }))
vi.mock('../ui', async () => {
  const { ref } = await import('vue')
  return { useIsMobile: () => ref(mobileFlag.value) }
})
vi.mock('element-plus', async () => {
  const real = await vi.importActual<any>('element-plus')
  return { ...real, ElMessageBox: { ...real.ElMessageBox, confirm: vi.fn().mockResolvedValue(''), prompt: vi.fn().mockResolvedValue({ value: '我的新模板' }) } }
})

function mockDefaults() {
  apiMock.mockImplementation((path: string) => {
    if (path === '/templates') return Promise.resolve(mockTemplates)
    if (path === '/categories') return Promise.resolve(['docker_registry'])
    return Promise.resolve(mockEntries[0])
  })
}

function setRoute(path: string, params: Record<string, string> = {}, query: Record<string, string> = {}) {
  routeMock.path = path
  routeMock.params = params
  routeMock.query = query
}

describe('EntryEdit.vue', () => {
  beforeEach(() => {
    apiMock.mockReset()
    mockDefaults()
    pushMock.mockReset()
    setRoute('/entries/new')
    mobileFlag.value = false
  })

  it('新建：渲染模板选择卡片网格（分组 + chips 预览）', async () => {
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('从模板开始')
    expect(w.text()).toContain('容器镜像')
    expect(w.text()).toContain('Docker 镜像仓库')
    expect(w.text()).toContain('空白开始')
    // 字段 chips 预览（敏感字段带锁样式类）
    expect(w.find('.pick-card').exists()).toBe(true)
    expect(w.find('.chip-sec').exists()).toBe(true)
  })

  it('点击模板卡：骨架与注释带入，进入表单', async () => {
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    const card = w.findAll('.pick-card').filter(c => c.text().includes('Docker 镜像仓库'))
    expect(card.length).toBe(1)
    await card[0].trigger('click')
    await flushPromises()
    expect(w.text()).toContain('基本信息')
    // 模板 2 个字段带入表单，说明进 desc 输入框 value
    expect(w.findAll('.field-row').length).toBe(2)
    const desc = w.find('input[placeholder="说明：这个字段是什么、怎么用（给 AI 看）"]')
    expect((desc.element as HTMLInputElement).value).toBe('仓库地址')
  })

  it('空白开始：单字段进入表单', async () => {
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text().includes('空白开始'))[0].trigger('click')
    await flushPromises()
    expect(w.findAll('.field-row').length).toBe(1)
  })

  it('复制新建 ?from=：拉源条目明文、标题加副本、跳过模板选择（边界）', async () => {
    setRoute('/entries/new', {}, { from: '1' })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/entries/1?reveal=true')
    expect(w.text()).toContain('基本信息') // 未渲染模板选择
    const titleInput = w.find('input[placeholder="如：华为SWR-杭州-生产"]')
    expect((titleInput.element as HTMLInputElement).value).toBe('生产ACR - 副本')
    expect(w.findAll('.field-row').length).toBe(2)
  })

  it('编辑路径：直接拉明文进表单，不出现模板选择', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).not.toContain('从模板开始')
    expect(w.text()).toContain('编辑：生产ACR')
  })

  it('保存校验：空标题拦截不发请求（边界）', async () => {
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '空白开始（完全自由定义）')[0].trigger('click')
    await flushPromises()
    apiMock.mockClear()
    await w.findAll('button').filter(b => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    expect(apiMock).not.toHaveBeenCalled()
  })

  it('新建保存：空分类默认 misc，POST 全量表单（边界）', async () => {
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '空白开始（完全自由定义）')[0].trigger('click')
    await flushPromises()
    const titleInput = w.find('input[placeholder="如：华为SWR-杭州-生产"]')
    await titleInput.setValue('新条目')
    apiMock.mockClear()
    await w.findAll('button').filter(b => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    const post = apiMock.mock.calls.find(c => c[1] && c[1].method === 'POST')
    expect(post).toBeTruthy()
    const body = JSON.parse(post![1].body)
    expect(body.title).toBe('新条目')
    expect(body.category).toBe('misc')
    expect(pushMock).toHaveBeenCalledWith('/entries')
  })

  it('编辑保存走 PUT /entries/:id', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockResolvedValue({})
    await w.findAll('button').filter(b => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    const put = apiMock.mock.calls.find(c => c[1] && c[1].method === 'PUT')
    expect(put).toBeTruthy()
    expect(put![0]).toBe('/entries/1')
  })

  it('字段未填说明时统计警告条（边界）', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    apiMock.mockImplementation((path: string) => {
      if (path === '/templates') return Promise.resolve(mockTemplates)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ ...mockEntries[0], fields: [
        { key: 'a', description: '有说明', type: 'text', is_secret: false, value: 'x' },
        { key: 'b', description: '', type: 'text', is_secret: false, value: 'y' },
      ] })
    })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('1 个字段未填写说明')
  })

  it('添加/删除字段行', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.findAll('.field-row').length).toBe(2)
    await w.findAll('button').filter(b => b.text() === '添加字段')[0].trigger('click')
    expect(w.findAll('.field-row').length).toBe(3)
    // 删除最后一行（circle 删除按钮在 .field-ops 内）
    const delBtns = w.findAll('.field-ops button')
    await delBtns[delBtns.length - 1].trigger('click')
    expect(w.findAll('.field-row').length).toBe(2)
  })

  it('另存为模板：prompt 后 POST save-as-template', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockResolvedValue({})
    await w.findAll('button').filter(b => b.text() === '另存为模板')[0].trigger('click')
    await flushPromises()
    const post = apiMock.mock.calls.find(c => String(c[0]).includes('save-as-template'))
    expect(post).toBeTruthy()
    expect(JSON.parse(post![1].body)).toEqual({ name: '我的新模板' })
  })

  it('TOTP 检测：字段含 totp 时显示动态码按钮（边界）', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    apiMock.mockImplementation((path: string) => {
      if (path === '/templates') return Promise.resolve(mockTemplates)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ ...mockEntries[0], fields: [
        { key: 'totp_secret', description: '两步验证密钥', type: 'text', is_secret: true, value: '***' },
      ] })
    })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.findAll('button').map(b => b.text())).toContain('动态码')
  })

  it('TOTP 弹窗：点击拉取动态码并倒计时，卸载清理定时器', async () => {
    vi.useFakeTimers()
    try {
      setRoute('/entries/1/edit', { id: '1' })
      apiMock.mockImplementation((path: string) => {
        if (path === '/templates') return Promise.resolve(mockTemplates)
        if (path === '/categories') return Promise.resolve([])
        if (path === '/entries/1/totp') return Promise.resolve({ code: '123456', remaining: 30 })
        return Promise.resolve({ ...mockEntries[0], fields: [
          { key: 'totp_secret', description: '两步验证密钥', type: 'text', is_secret: true, value: '***' },
        ] })
      })
      const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
      await flushPromises()
      apiMock.mockClear()
      await w.findAll('button').filter(b => b.text() === '动态码')[0].trigger('click')
      await flushPromises()
      expect(apiMock).toHaveBeenCalledWith('/entries/1/totp', expect.objectContaining({ method: 'POST' }))
      expect(w.text()).toContain('123456')
      // 倒计时推进 1 秒（fake timer）
      await vi.advanceTimersByTimeAsync(1000)
      expect(w.text()).toContain('29 秒')
      w.unmount() // 清理 interval 不崩
    } finally {
      vi.useRealTimers()
    }
  })

  it('标签添加：回车确认、重复去重（边界）', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    // 点击「标签」按钮出现输入框
    await w.findAll('button').filter(b => b.text() === '标签')[0].trigger('click')
    await flushPromises()
    const tagInput = w.find('input[placeholder="回车确认"]')
    expect(tagInput.exists()).toBe(true)
    await tagInput.setValue('新标签')
    await tagInput.trigger('keyup.enter')
    await flushPromises()
    expect(w.text()).toContain('新标签')
    // 再添加重复标签 → 不重复渲染两个
    await w.findAll('button').filter(b => b.text() === '标签')[0].trigger('click')
    await flushPromises()
    const tagInput2 = w.find('input[placeholder="回车确认"]')
    await tagInput2.setValue('新标签')
    await tagInput2.trigger('keyup.enter')
    await flushPromises()
    expect(w.text().match(/新标签/g)!.length).toBe(1)
  })

  it('TOTP 拉取失败：提示并自动关闭弹窗（catch 分支）', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    apiMock.mockImplementation((path: string) => {
      if (path === '/templates') return Promise.resolve(mockTemplates)
      if (path === '/categories') return Promise.resolve([])
      if (path === '/entries/1/totp') return Promise.reject(new Error('非 TOTP 字段'))
      return Promise.resolve({ ...mockEntries[0], fields: [
        { key: 'totp_secret', description: '两步验证密钥', type: 'text', is_secret: true, value: '***' },
      ] })
    })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    await w.findAll('button').filter(b => b.text() === '动态码')[0].trigger('click')
    await flushPromises()
    // 拉取失败：code 不渲染、页面不崩（弹窗关闭动画中文本可能残留，以内容为准）
    expect(w.text()).not.toContain('123456')
  })

  it('保存失败：提示错误且不跳转（catch 分支）', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    apiMock.mockClear()
    apiMock.mockRejectedValue(new Error('主密钥校验失败'))
    await w.findAll('button').filter(b => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    expect(pushMock).not.toHaveBeenCalled()
  })

  it('字段类型渲染分支：多行=textarea、敏感=password（边界）', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    apiMock.mockImplementation((path: string) => {
      if (path === '/templates') return Promise.resolve(mockTemplates)
      if (path === '/categories') return Promise.resolve([])
      return Promise.resolve({ ...mockEntries[0], fields: [
        { key: 'note', description: '备注', type: 'multiline', is_secret: false, value: 'x' },
        { key: 'pwd', description: '密码', type: 'text', is_secret: true, value: '***' },
        { key: 'plain', description: '普通', type: 'text', is_secret: false, value: 'y' },
      ] })
    })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.find('textarea').exists()).toBe(true) // multiline
    expect(w.find('input[type="password"]').exists()).toBe(true) // 敏感
    expect(w.find('input[placeholder="值"]').exists()).toBe(true) // 普通文本
  })

  it('字段行编辑交互：改字段名/说明/敏感后保存体反映（v-model 全链路）', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    const row = w.findAll('.field-row')[0]
    await row.find('input[placeholder="字段名，如 ak / registry_url"]').setValue('renamed_key')
    await row.find('input[placeholder="说明：这个字段是什么、怎么用（给 AI 看）"]').setValue('新说明')
    await row.find('input[type="checkbox"]').setValue(true) // 勾敏感
    // 标签输入框（基本信息区）与说明 textarea
    await w.find('textarea').setValue('新的条目说明')
    apiMock.mockClear()
    apiMock.mockResolvedValue({})
    await w.findAll('button').filter(b => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    const put = apiMock.mock.calls.find(c => c[1] && c[1].method === 'PUT')
    const body = JSON.parse(put![1].body)
    expect(body.description).toBe('新的条目说明')
    expect(body.fields[0].key).toBe('renamed_key')
    expect(body.fields[0].description).toBe('新说明')
    expect(body.fields[0].is_secret).toBe(true)
  })

  it('标签删除（close 回调）与返回按钮', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    // 返回按钮用 $router 全局属性，注入桩
    const routerStub = { install: (app: any) => { app.config.globalProperties.$router = { push: pushMock } } }
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus, routerStub] } })
    await flushPromises()
    // mockEntries[0] 带两个标签，删掉「生产」标签（按内容定位它的关闭 X）
    const tags = w.findAll('.el-tag').filter(t => t.text().includes('生产'))
    expect(tags.length).toBe(1)
    await tags[0].find('.el-tag__close').trigger('click')
    await flushPromises()
    // 只剩「内网」一个标签
    expect(w.findAll('.el-tag').filter(t => t.text().trim() === '内网').length).toBe(1)
    expect(w.findAll('.el-tag').filter(t => t.text().includes('生产')).length).toBe(0)
    // 返回按钮 → /entries
    await w.findAll('button').filter(b => b.text() === '返回')[0].trigger('click')
    expect(pushMock).toHaveBeenCalledWith('/entries')
  })

  it('AI 可见开关切换与不可见提示（switch v-model + hint 分支）', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).toContain('允许持有令牌的 AI 读取此条目')
    await w.find('.el-switch').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('对 AI 完全隐身')
  })

  it('移动端：label 顶部排列 + 字段行移动布局（field-row-m 分支）', async () => {
    mobileFlag.value = true
    setRoute('/entries/1/edit', { id: '1' })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.find('.field-row-m').exists()).toBe(true)
    expect(w.text()).toContain('基本信息')
    expect(w.text()).toContain('编辑：生产ACR')
  })

  it('加载失败：提示错误且表单区不崩（catch 分支）', async () => {
    setRoute('/entries/1/edit', { id: '1' })
    apiMock.mockImplementation((path: string) => {
      if (path === '/templates') return Promise.resolve(mockTemplates)
      if (path === '/categories') return Promise.resolve([])
      return Promise.reject(new Error('无权访问'))
    })
    const w = mount(EntryEdit, { global: { plugins: [ElementPlus] } })
    await flushPromises()
    expect(w.text()).not.toContain('编辑：生产ACR') // 数据未载入
  })
})
