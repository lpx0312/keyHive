<template>
  <div>
    <!-- 页头 -->
    <div class="page-head">
      <div class="page-title">
        <h3>录入模板</h3>
        <p class="tip">内置模板覆盖常见运维场景；在条目编辑页「另存为模板」可沉淀自己的结构。</p>
      </div>
      <div class="page-actions">
        <el-input v-model="q" placeholder="搜索模板 / 字段" clearable :prefix-icon="Search" class="search" />
        <el-button type="primary" :icon="Plus" @click="openDlg()">新增模板</el-button>
      </div>
    </div>

    <!-- 分组 + 卡片网格 -->
    <div v-if="filteredGroups.length" class="groups">
      <section v-for="g in filteredGroups" :key="g.name" class="group">
        <div class="group-head">
          <span class="group-name">{{ g.name }}</span>
          <span class="group-count">{{ g.items.length }}</span>
        </div>
        <div class="grid">
          <article v-for="row in g.items" :key="row.id" class="tpl-card">
            <div class="tpl-top">
              <span class="tpl-name" :title="row.name">{{ row.name }}</span>
              <el-tag v-if="!row.builtin" size="small" type="success" effect="light" round>自建</el-tag>
            </div>
            <code class="tpl-cat">{{ row.category }}</code>
            <ul class="tpl-fields">
              <li v-for="f in row.fields" :key="f.key" class="fld">
                <code class="fld-key" :title="f.key">{{ f.key }}</code>
                <span class="fld-desc" :title="f.description">{{ f.description }}</span>
                <span v-if="f.type !== 'text'" class="fld-type">{{ typeLabel(f.type) }}</span>
                <el-tooltip v-if="f.is_secret" content="敏感字段，加密存储">
                  <el-icon class="fld-lock"><Lock /></el-icon>
                </el-tooltip>
              </li>
            </ul>
            <div class="tpl-ops">
              <el-button text size="small" :icon="EditPen" @click="openDlg(row)">编辑</el-button>
              <el-button v-if="!row.builtin" text size="small" type="danger" :icon="Delete" @click="del(row)">删除</el-button>
            </div>
          </article>
        </div>
      </section>
    </div>
    <el-empty v-else :description="q ? '没有匹配的模板' : '暂无模板'" />

    <!-- 新增/编辑模板弹窗 -->
    <el-dialog v-model="dlg" :title="form.id ? `编辑模板：${form.name}` : '新增模板'" width="720px" top="6vh" class="tpl-dlg">
      <div class="dlg-basics">
        <el-form label-position="top" class="basics-form">
          <div class="basics-grid">
            <el-form-item label="模板名" required>
              <el-input v-model="form.name" placeholder="如：Zabbix 监控" maxlength="60" />
            </el-form-item>
            <el-form-item label="分组">
              <el-input v-model="form.group" placeholder="默认「我的模板」" maxlength="30" />
            </el-form-item>
            <el-form-item label="标识">
              <el-input v-model="form.category" placeholder="条目分类标识，如 my_db（留空自动生成）" maxlength="40" />
            </el-form-item>
          </div>
        </el-form>
      </div>

      <div class="sec-head">
        <span class="sec-title">字段骨架<em v-if="form.fields.length"> · {{ form.fields.length }} 个</em></span>
        <el-button size="small" :icon="Plus" @click="addField">添加字段</el-button>
      </div>
      <p class="sec-tip">每个字段 = 名称 + 给 AI 的说明；勾选「敏感」的值会加密存储、只在需要时 reveal。</p>

      <div v-if="form.fields.length" class="frows">
        <div v-for="(f, i) in form.fields" :key="i" class="frow">
          <span class="frow-num">{{ i + 1 }}</span>
          <div class="frow-main">
            <div class="frow-line1">
              <div class="frow-key">
                <label>字段名</label>
                <el-input v-model="f.key" placeholder="英文，如 access_key_id" maxlength="60" />
              </div>
              <el-select v-model="f.type" class="frow-type">
                <el-option label="文本" value="text" />
                <el-option label="网址" value="url" />
                <el-option label="多行" value="multiline" />
              </el-select>
              <el-checkbox v-model="f.is_secret" class="frow-sec">敏感</el-checkbox>
              <el-tooltip content="删除该字段" placement="top">
                <el-button :icon="Close" circle size="small" class="frow-del" @click="form.fields.splice(i, 1)" />
              </el-tooltip>
            </div>
            <div class="frow-desc">
              <label>给 AI 的说明</label>
              <el-input v-model="f.description" placeholder="如：控制台登录地址" maxlength="200" />
            </div>
          </div>
        </div>
      </div>
      <div v-else class="frows-empty">尚未添加字段，点上方「添加字段」开始搭建骨架。</div>

      <template #footer>
        <el-button @click="dlg = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Delete, EditPen, Lock, Close, Search } from '@element-plus/icons-vue'
import { api, Template, Field } from '../api'

const templates = ref<Template[]>([])
const dlg = ref(false)
const saving = ref(false)
const q = ref('')

interface TplForm {
  id: number
  name: string
  group: string
  category: string
  fields: Field[]
}

const form = ref<TplForm>({ id: 0, name: '', group: '', category: '', fields: [] })

function typeLabel(t: string) {
  return t === 'url' ? '网址' : t === 'multiline' ? '多行' : ''
}

const groups = computed(() => {
  const m: Record<string, Template[]> = {}
  for (const t of templates.value) (m[t.group] ||= []).push(t)
  return Object.entries(m).map(([name, items]) => ({ name, items }))
})

// 搜索：匹配模板名/分组/标识/字段名/字段说明
const filteredGroups = computed(() => {
  const kw = q.value.trim().toLowerCase()
  if (!kw) return groups.value
  return groups.value
    .map((g) => ({
      name: g.name,
      items: g.items.filter((t) =>
        t.name.toLowerCase().includes(kw) ||
        t.group.toLowerCase().includes(kw) ||
        t.category.toLowerCase().includes(kw) ||
        t.fields.some((f) => f.key.toLowerCase().includes(kw) || f.description.toLowerCase().includes(kw))),
    }))
    .filter((g) => g.items.length)
})

async function load() {
  templates.value = await api('/templates')
}

function openDlg(row?: Template) {
  if (row) {
    // 深拷贝，取消编辑不污染列表
    form.value = {
      id: row.id, name: row.name, group: row.group, category: row.category,
      fields: row.fields.map(f => ({ ...f })),
    }
  } else {
    form.value = { id: 0, name: '', group: '', category: '', fields: [{ key: '', description: '', type: 'text', is_secret: false, value: '' }] }
  }
  dlg.value = true
}

function addField() {
  form.value.fields.push({ key: '', description: '', type: 'text', is_secret: false, value: '' })
}

async function save() {
  if (!form.value.name.trim()) { ElMessage.warning('模板名不能为空'); return }
  const fields = form.value.fields.filter(f => f.key.trim())
  if (!fields.length) { ElMessage.warning('至少保留一个字段'); return }
  for (const f of fields) {
    if (!f.description.trim()) { ElMessage.warning(`字段「${f.key}」缺少给 AI 的说明`); return }
  }
  const body = JSON.stringify({ name: form.value.name, group: form.value.group, category: form.value.category, fields })
  saving.value = true
  try {
    if (form.value.id) {
      await api(`/templates/${form.value.id}`, { method: 'PUT', body })
      ElMessage.success('模板已更新')
    } else {
      await api('/templates', { method: 'POST', body })
      ElMessage.success('模板已创建')
    }
    dlg.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function del(row: Template) {
  await ElMessageBox.confirm(`确定删除模板「${row.name}」？`, '删除确认', { type: 'warning' })
  await api(`/templates/${row.id}`, { method: 'DELETE' })
  ElMessage.success('已删除')
  load()
}

onMounted(load)
</script>

<style scoped>
/* ---- 页头 ---- */
.page-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; flex-wrap: wrap; margin-bottom: 20px; }
.page-title h3 { margin: 0 0 4px; font-size: 19px; }
.tip { color: #94a3b8; font-size: 13px; margin: 0; }
.page-actions { display: flex; gap: 10px; align-items: center; }
.search { width: 220px; }

/* ---- 分组 ---- */
.group { margin-bottom: 22px; }
.group-head { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; }
.group-name { font-size: 14px; font-weight: 600; color: #334155; }
.group-count { min-width: 20px; height: 20px; padding: 0 6px; border-radius: 10px; background: #eef1f6; color: #64748b; font-size: 12px; display: inline-flex; align-items: center; justify-content: center; }

/* ---- 卡片网格 ---- */
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(350px, 1fr)); gap: 12px; }
.tpl-card { background: #fff; border: 1px solid #e6eaf2; border-radius: 10px; padding: 14px 16px 8px; display: flex; flex-direction: column; transition: border-color .2s, box-shadow .2s, transform .2s; }
.tpl-card:hover { border-color: #a0cfff; box-shadow: 0 4px 14px rgba(29, 41, 57, .07); transform: translateY(-1px); }
.tpl-top { display: flex; align-items: center; gap: 8px; }
.tpl-name { font-size: 15px; font-weight: 600; color: #1d2939; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tpl-cat { align-self: flex-start; margin-top: 6px; font-size: 12px; color: #64748b; background: #f1f5f9; border-radius: 4px; padding: 1px 6px; }
.tpl-fields { list-style: none; margin: 10px 0 0; padding: 0; flex: 1; }
.fld { display: flex; align-items: flex-start; gap: 8px; padding: 3px 0; font-size: 13px; }
.fld-key { flex-shrink: 0; max-width: 45%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; color: #2563eb; background: #eff6ff; border-radius: 4px; padding: 1px 6px; }
.fld-desc { flex: 1; min-width: 0; color: #64748b; line-height: 1.45; }
.fld-type { flex-shrink: 0; margin-top: 2px; font-size: 11px; color: #94a3b8; border: 1px solid #e2e8f0; border-radius: 4px; padding: 0 5px; line-height: 18px; }
.fld-lock { flex-shrink: 0; margin-top: 2px; color: #e6a23c; font-size: 13px; }
.tpl-ops { display: flex; justify-content: flex-end; gap: 2px; margin-top: 6px; padding-top: 4px; border-top: 1px dashed #eef1f6; opacity: .75; }
.tpl-card:hover .tpl-ops { opacity: 1; }

/* ---- 弹窗 ---- */
.basics-grid { display: grid; grid-template-columns: 1.2fr 1fr 1.2fr; gap: 0 14px; }
.basics-form :deep(.el-form-item) { margin-bottom: 12px; }
.basics-form :deep(.el-form-item__label) { padding-bottom: 2px; font-size: 13px; color: #475569; }
.sec-head { display: flex; align-items: center; justify-content: space-between; margin: 18px 0 4px; }
.sec-title { font-size: 14px; font-weight: 600; color: #1d2939; }
.sec-title em { font-style: normal; font-weight: 400; color: #94a3b8; font-size: 13px; }
.sec-tip { margin: 0 0 10px; font-size: 12px; color: #94a3b8; }
.frows { max-height: 46vh; overflow: auto; display: flex; flex-direction: column; gap: 8px; padding-right: 2px; }
.frow { display: flex; gap: 10px; background: #f8fafc; border: 1px solid #eef1f6; border-radius: 8px; padding: 10px 12px; }
.frow-num { flex-shrink: 0; width: 22px; height: 22px; margin-top: 24px; border-radius: 50%; background: #e3edff; color: #2563eb; font-size: 12px; font-weight: 600; display: flex; align-items: center; justify-content: center; }
.frow-main { flex: 1; min-width: 0; }
.frow-line1 { display: flex; align-items: flex-end; gap: 8px; }
.frow-key { flex: 1; min-width: 0; }
.frow-key label, .frow-desc label { display: block; font-size: 11px; color: #94a3b8; line-height: 18px; }
.frow-type { width: 92px; flex-shrink: 0; }
.frow-sec { flex-shrink: 0; margin: 0 2px 0 4px; }
.frow-del { flex-shrink: 0; margin-left: 2px; }
.frow-desc { margin-top: 6px; }
.frows-empty { padding: 22px 0; text-align: center; font-size: 13px; color: #94a3b8; background: #f8fafc; border: 1px dashed #e2e8f0; border-radius: 8px; }

@media (max-width: 768px) {
  .page-actions { width: 100%; }
  .search { flex: 1; width: auto; }
  .grid { grid-template-columns: 1fr; }
  .basics-grid { grid-template-columns: 1fr; }
  .frow-line1 { flex-wrap: wrap; }
  .frow-key { flex-basis: 100%; }
  .frow-type { flex: 1; }
  .frow-num { margin-top: 0; }
}
</style>
