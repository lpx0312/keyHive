<template>
  <div>
    <h3>录入模板</h3>
    <div class="head-row">
      <p class="tip">内置模板覆盖常见运维场景；在条目编辑页「另存为模板」可沉淀自己的结构。</p>
      <el-button type="primary" @click="openDlg()">＋ 新增模板</el-button>
    </div>
    <el-collapse v-model="open">
      <el-collapse-item v-for="g in groups" :key="g.name" :name="g.name" :title="`${g.name}（${g.items.length}）`">
        <el-table :data="g.items" size="small">
          <el-table-column prop="name" label="模板名" width="200">
            <template #default="{ row }">
              {{ row.name }}
              <el-tag v-if="!row.builtin" size="small" type="success">自建</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="category" label="标识" width="160" />
          <el-table-column label="字段骨架（名称 → 给 AI 的说明）">
            <template #default="{ row }">
              <div v-for="f in row.fields" :key="f.key" class="f">
                <code>{{ f.key }}</code>
                <span class="desc">{{ f.description }}</span>
                <el-tag v-if="f.is_secret" size="small" type="warning">敏感</el-tag>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="140">
            <template #default="{ row }">
              <el-button size="small" @click="openDlg(row)">编辑</el-button>
              <el-button v-if="!row.builtin" size="small" type="danger" @click="del(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-collapse-item>
    </el-collapse>

    <!-- 新增/编辑模板弹窗 -->
    <el-dialog v-model="dlg" :title="form.id ? `编辑模板：${form.name}` : '新增模板'" width="640px" top="6vh">
      <el-form label-width="80px">
        <el-form-item label="模板名" required>
          <el-input v-model="form.name" placeholder="如：Zabbix 监控" maxlength="60" />
        </el-form-item>
        <el-form-item label="分组">
          <el-input v-model="form.group" placeholder="默认「我的模板」" maxlength="30" />
        </el-form-item>
        <el-form-item label="标识">
          <el-input v-model="form.category" placeholder="条目分类标识，如 my_db（留空自动生成）" maxlength="40" />
        </el-form-item>
        <el-form-item label="字段骨架">
          <div class="flds">
            <div v-for="(f, i) in form.fields" :key="i" class="fld-row">
              <el-input v-model="f.key" placeholder="字段名（英文）" class="w-key" />
              <el-select v-model="f.type" class="w-type">
                <el-option label="文本" value="text" />
                <el-option label="网址" value="url" />
                <el-option label="多行" value="multiline" />
              </el-select>
              <el-checkbox v-model="f.is_secret">敏感</el-checkbox>
              <el-input v-model="f.description" placeholder="给 AI 的说明（如：控制台登录地址）" class="w-desc" />
              <el-button size="small" type="danger" plain circle @click="form.fields.splice(i, 1)">✕</el-button>
            </div>
            <el-button size="small" @click="addField">＋ 添加字段</el-button>
          </div>
        </el-form-item>
      </el-form>
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
import { api, Template, Field } from '../api'

const templates = ref<Template[]>([])
const open = ref<string[]>([])
const dlg = ref(false)
const saving = ref(false)

interface TplForm {
  id: number
  name: string
  group: string
  category: string
  fields: Field[]
}

const form = ref<TplForm>({ id: 0, name: '', group: '', category: '', fields: [] })

const groups = computed(() => {
  const m: Record<string, Template[]> = {}
  for (const t of templates.value) (m[t.group] ||= []).push(t)
  return Object.entries(m).map(([name, items]) => ({ name, items }))
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
.head-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.tip { color: #94a3b8; font-size: 13px; }
.f { margin: 2px 0; }
.f code { background: #f1f5f9; padding: 1px 6px; border-radius: 3px; margin-right: 8px; }
.f .desc { color: #64748b; margin-right: 6px; }
.flds { width: 100%; }
.fld-row { display: flex; gap: 8px; align-items: center; margin-bottom: 8px; }
.w-key { width: 150px; }
.w-type { width: 90px; }
.w-desc { flex: 1; }
</style>
