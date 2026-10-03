<template>
  <div v-loading="loading">
    <!-- 模板选择（仅新建且未选时） -->
    <el-card v-if="isCreate && !templatePicked" class="tpl-card">
      <template #header>从模板开始（可选，骨架与注释会自动带出，之后仍可自由增删改）</template>
      <el-collapse v-model="openGroups">
        <el-collapse-item v-for="g in groupedTemplates" :key="g.name" :name="g.name" :title="`${g.name}（${g.items.length}）`">
          <el-button v-for="t in g.items" :key="t.id" class="tpl-btn" @click="applyTemplate(t)">
            {{ t.name }}<el-tag v-if="!t.builtin" size="small" type="success" style="margin-left: 6px">自建</el-tag>
          </el-button>
        </el-collapse-item>
      </el-collapse>
      <el-button type="primary" plain style="margin-top: 12px" @click="startBlank">空白开始（完全自由定义）</el-button>
    </el-card>

    <!-- 编辑表单 -->
    <div v-else>
      <div class="head">
        <h3>{{ isCreate ? '新建条目' : '编辑：' + form.title }}</h3>
        <div>
          <el-button v-if="!isCreate && hasTOTP" @click="showTOTP">🔑 动态码</el-button>
          <el-button v-if="!isCreate" @click="saveAsTemplate">另存为模板</el-button>
          <el-button type="primary" :loading="saving" @click="save">保存</el-button>
          <el-button @click="$router.push('/entries')">返回</el-button>
        </div>
      </div>

      <el-card>
        <el-form :label-position="isMobile ? 'top' : 'right'" :label-width="isMobile ? '' : '90px'">
          <el-form-item label="标题" required>
            <el-input v-model="form.title" placeholder="如：华为SWR-杭州-生产" />
          </el-form-item>
          <el-form-item label="分类">
            <el-select v-model="form.category" filterable allow-create default-first-option placeholder="选择或输入新分类">
              <el-option v-for="c in categories" :key="c" :label="c" :value="c" />
            </el-select>
          </el-form-item>
          <el-form-item label="标签">
            <div class="tags-box">
              <el-tag v-for="tag in form.tags || []" :key="tag" closable @close="form.tags = (form.tags || []).filter((t) => t !== tag)">{{ tag }}</el-tag>
              <el-input v-if="tagInputVisible" ref="tagInputRef" v-model="tagInputValue" size="small" style="width: 110px"
                placeholder="回车确认" @keyup.enter="addTag" @blur="addTag" />
              <el-button v-else size="small" @click="showTagInput">＋ 标签</el-button>
            </div>
            <span class="hint" style="margin-left: 8px">如：内网 / 公司内网 / 公网，列表可按标签筛选</span>
          </el-form-item>
          <el-form-item label="条目说明">
            <el-input v-model="form.description" type="textarea" :rows="2"
              placeholder="这条凭据是干嘛的、在哪个环境用 —— 给 AI 看的上下文，强烈建议填写" />
          </el-form-item>
          <el-form-item label="AI 可见">
            <el-switch v-model="form.ai_visible" />
            <span class="hint">{{ form.ai_visible ? '允许持有令牌的 AI 读取此条目（敏感值仍需 reveal）' : '对 AI 完全隐身：列表中不出现，任何令牌都无法访问' }}</span>
          </el-form-item>
        </el-form>
      </el-card>

      <el-card class="fields-card">
        <template #header>
          字段（每行 = 字段名 / 类型 / 值 / 给 AI 的说明）
          <el-button size="small" style="float: right" @click="addField">＋ 添加字段</el-button>
        </template>

        <div v-for="(f, i) in form.fields" :key="i" :class="['field-row', { 'field-row-m': isMobile }]">
          <el-input v-model="f.key" placeholder="字段名，如 ak / registry_url" class="c-key" />
          <el-select v-model="f.type" class="c-type">
            <el-option label="文本" value="text" />
            <el-option label="URL" value="url" />
            <el-option label="多行" value="multiline" />
          </el-select>
          <el-input v-if="f.type === 'multiline'" v-model="f.value" type="textarea" :rows="2" class="c-val" placeholder="值" />
          <el-input v-else-if="f.is_secret" v-model="f.value" type="password" show-password class="c-val" placeholder="值（敏感，加密存储）" />
          <el-input v-else v-model="f.value" class="c-val" placeholder="值" />
          <el-input v-model="f.description" class="c-desc"
            :class="{ 'desc-warn': !f.description }"
            placeholder="说明：这个字段是什么、怎么用（给 AI 看）" />
          <div class="field-ops">
            <el-checkbox v-model="f.is_secret" class="c-sec">敏感</el-checkbox>
            <el-button :icon="Delete" circle size="small" @click="form.fields.splice(i, 1)" />
          </div>
        </div>
        <el-alert v-if="missingDesc" type="warning" :closable="false" style="margin-top: 8px"
          :title="`有 ${missingDesc} 个字段未填写说明：AI 将无法准确理解这些字段的用途`" />
      </el-card>
    </div>

    <!-- 两步验证动态码 -->
    <el-dialog v-model="totpDlg" title="两步验证动态码" width="340px" @closed="stopTOTPTimer">
      <div class="totp-code">{{ totpCode }}</div>
      <el-progress :percentage="totpRemaining / 30 * 100" :show-text="false" :stroke-width="6"
        :color="totpRemaining <= 5 ? '#f56c6c' : '#409eff'" />
      <p class="totp-hint">{{ totpRemaining }} 秒后刷新（服务端生成，密钥不出库，已记审计）</p>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete } from '@element-plus/icons-vue'
import { api, Entry, Field, Template } from '../api'
import { useIsMobile } from '../ui'

const route = useRoute()
const router = useRouter()
const isMobile = useIsMobile()
const isCreate = route.path === '/entries/new'
const id = Number(route.params.id || 0)

const loading = ref(false)
const saving = ref(false)
const templatePicked = ref(!isCreate)
const openGroups = ref<string[]>([])

const form = ref<Entry>(blankEntry())
const templates = ref<Template[]>([])
const categories = ref<string[]>([])

function blankEntry(): Entry {
  return { id: 0, title: '', category: '', description: '', tags: [], ai_visible: true, fields: [], created_at: '', updated_at: '' }
}

const groupedTemplates = computed(() => {
  const groups: Record<string, Template[]> = {}
  for (const t of templates.value) (groups[t.group] ||= []).push(t)
  return Object.entries(groups).map(([name, items]) => ({ name, items }))
})

const missingDesc = computed(() => form.value.fields.filter((f) => !f.description.trim()).length)

// ---- 标签输入（回车/失焦确认，去重） ----
const tagInputVisible = ref(false)
const tagInputValue = ref('')
const tagInputRef = ref()
function showTagInput() {
  tagInputVisible.value = true
  nextTick(() => tagInputRef.value?.focus())
}
function addTag() {
  const v = tagInputValue.value.trim()
  if (v && !(form.value.tags || []).includes(v)) form.value.tags = [...(form.value.tags || []), v]
  tagInputVisible.value = false
  tagInputValue.value = ''
}

function applyTemplate(t: Template) {
  form.value.category = t.category === 'blank' ? '' : t.category
  form.value.title = ''
  form.value.fields = t.fields.map((f) => ({ ...f, value: '' }))
  templatePicked.value = true
}

function startBlank() {
  form.value.fields = [{ key: '', description: '', type: 'text', is_secret: false, value: '' }]
  templatePicked.value = true
}

function addField() {
  form.value.fields.push({ key: '', description: '', type: 'text', is_secret: false, value: '' })
}

async function load() {
  loading.value = true
  try {
    templates.value = await api('/templates')
    categories.value = await api('/categories')
    if (!isCreate) {
      // 编辑时直接取明文（服务端已记 entry_reveal 审计）
      form.value = await api(`/entries/${id}?reveal=true`)
      if (!form.value.tags) form.value.tags = []
    }
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!form.value.title.trim()) return ElMessage.warning('标题不能为空')
  if (!form.value.category.trim()) form.value.category = 'misc'
  saving.value = true
  try {
    if (isCreate) {
      await api('/entries', { method: 'POST', body: JSON.stringify(form.value) })
    } else {
      await api(`/entries/${id}`, { method: 'PUT', body: JSON.stringify(form.value) })
    }
    ElMessage.success('已保存')
    router.push('/entries')
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function saveAsTemplate() {
  const { value } = await ElMessageBox.prompt('模板名称', '另存为模板', {
    inputValue: form.value.title + ' 模板',
  })
  await api(`/entries/${id}/save-as-template`, { method: 'POST', body: JSON.stringify({ name: value }) })
  ElMessage.success('已存为模板（字段骨架与注释，不含值）')
}

// ---- 两步验证动态码（服务端生成，密钥不出库） ----
const hasTOTP = computed(() => form.value.fields.some(
  (f) => f.key.toLowerCase().includes('totp') || f.description.includes('TOTP') || f.description.includes('两步验证')))
const totpDlg = ref(false)
const totpCode = ref('')
const totpRemaining = ref(0)
let totpTimer: number | undefined

async function fetchTOTP() {
  try {
    const r = await api(`/entries/${id}/totp`, { method: 'POST', body: JSON.stringify({}) })
    totpCode.value = r.code
    totpRemaining.value = r.remaining
  } catch (e: any) {
    ElMessage.error(e.message)
    stopTOTPTimer()
    totpDlg.value = false
  }
}

function showTOTP() {
  totpDlg.value = true
  fetchTOTP()
  stopTOTPTimer()
  totpTimer = window.setInterval(() => {
    totpRemaining.value--
    if (totpRemaining.value <= 0) fetchTOTP() // 到期自动取下一窗口
  }, 1000)
}

function stopTOTPTimer() {
  if (totpTimer) { clearInterval(totpTimer); totpTimer = undefined }
}

onUnmounted(stopTOTPTimer)

onMounted(load)
</script>

<style scoped>
.head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; gap: 8px; flex-wrap: wrap; }
.tpl-card { margin-bottom: 16px; }
.tpl-btn { margin: 4px; }
.fields-card { margin-top: 16px; }
.field-row { display: flex; gap: 8px; align-items: flex-start; margin-bottom: 8px; }
.c-key { width: 200px; flex-shrink: 0; }
.c-type { width: 90px; flex-shrink: 0; }
.c-val { flex: 1; }
.c-desc { flex: 1.6; }
.field-ops { display: flex; align-items: center; gap: 8px; flex-shrink: 0; }
.c-sec { flex-shrink: 0; margin-right: 0; }
.desc-warn :deep(.el-input__wrapper) { box-shadow: 0 0 0 1px #e6a23c inset; }
.hint { margin-left: 10px; color: #94a3b8; font-size: 12px; }
.totp-code { font-size: 40px; font-weight: 700; letter-spacing: 8px; text-align: center; font-family: monospace; }
.totp-hint { color: #94a3b8; font-size: 12px; text-align: center; }

/* 移动端：字段卡片（字段名+类型一行，值/说明/操作各占整行） */
.field-row-m { display: grid; grid-template-columns: 58% 1fr; gap: 6px; border: 1px solid #e4e7ed; border-radius: 8px; padding: 10px; margin-bottom: 10px; background: #fafbfc; }
.field-row-m .c-val, .field-row-m .c-desc, .field-row-m .field-ops { grid-column: 1 / -1; }

.tags-box { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
</style>
