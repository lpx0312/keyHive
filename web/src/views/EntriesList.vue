<template>
  <div>
    <div class="toolbar">
      <el-input v-model="q" placeholder="搜索标题 / 说明 / 分类" clearable style="width: 260px" @input="load" />
      <el-select v-model="category" placeholder="全部分类" clearable style="width: 180px" @change="load">
        <el-option v-for="c in categories" :key="c" :label="c" :value="c" />
      </el-select>
      <el-button type="primary" @click="$router.push('/entries/new')">＋ 新建条目</el-button>
      <el-button v-if="me.isAdmin" @click="impDlg = true">导入</el-button>
      <el-button v-if="me.isAdmin" @click="expDlg = true">导出</el-button>
    </div>

    <!-- 移动端：卡片列表 -->
    <div v-if="isMobile" v-loading="loading" class="cards">
      <el-empty v-if="!loading && !entries.length" description="暂无条目" />
      <el-card v-for="row in entries" :key="row.id" class="card" shadow="hover" @click="open(row)">
        <div class="card-head">
          <b>{{ row.title }}</b>
          <el-tag v-if="!row.ai_visible" size="small" type="warning">AI 不可见</el-tag>
        </div>
        <div class="card-meta">
          <el-tag size="small">{{ row.category }}</el-tag>
          <span class="card-fields">{{ row.fields.length }} 字段<template v-if="secretCount(row)"> · 🔒{{ secretCount(row) }}</template></span>
          <span v-if="isStale(row.updated_at)" class="stale">⚠️ {{ daysSince(row.updated_at) }} 天未更新</span>
        </div>
        <div v-if="row.description" class="card-desc">{{ row.description }}</div>
        <div class="card-actions">
          <el-button size="small" @click.stop="open(row)">编辑</el-button>
          <el-button size="small" type="danger" @click.stop="del(row)">删除</el-button>
        </div>
      </el-card>
    </div>

    <!-- 桌面：表格 -->
    <el-table v-else :data="entries" v-loading="loading" @row-click="open">
      <el-table-column prop="title" label="标题" min-width="180">
        <template #default="{ row }">
          <b>{{ row.title }}</b>
          <el-tag v-if="!row.ai_visible" size="small" type="warning" style="margin-left: 6px">AI 不可见</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="category" label="分类" width="150">
        <template #default="{ row }">
          <el-tag size="small">{{ row.category }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="description" label="说明" min-width="200" show-overflow-tooltip />
      <el-table-column label="字段" width="80">
        <template #default="{ row }">
          {{ row.fields.length }} 个<el-tooltip content="含敏感字段数"><span v-if="secretCount(row)" class="sec">（🔒{{ secretCount(row) }}）</span></el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="updated_at" label="更新时间" width="170">
        <template #default="{ row }">
          <el-tooltip v-if="isStale(row.updated_at)" placement="top"
            :content="`已 ${daysSince(row.updated_at)} 天未更新，建议轮换密码`">
            <span class="stale">{{ fmtTime(null, null, row.updated_at) }} ⚠️</span>
          </el-tooltip>
          <span v-else>{{ fmtTime(null, null, row.updated_at) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="130" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click.stop="open(row)">编辑</el-button>
          <el-button size="small" type="danger" @click.stop="del(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 导出弹窗 -->
    <el-dialog v-model="expDlg" title="导出全库" width="440px">
      <el-radio-group v-model="expMode">
        <el-radio value="masked">遮蔽版（敏感值为 ***，可分享结构）</el-radio><br />
        <el-radio value="plain">明文版（含敏感值，仅用于迁移/备份）</el-radio>
      </el-radio-group>
      <el-alert v-if="expMode === 'plain'" type="warning" :closable="false" style="margin-top: 12px"
        title="明文导出含全部密码，操作已记审计；文件请勿提交仓库/长期留存" />
      <template #footer>
        <el-button @click="expDlg = false">取消</el-button>
        <el-button type="primary" :loading="exping" @click="doExport">下载 JSON</el-button>
      </template>
    </el-dialog>

    <!-- 导入弹窗 -->
    <el-dialog v-model="impDlg" title="导入" width="520px">
      <el-form label-width="90px">
        <el-form-item label="来源格式">
          <el-select v-model="impFormat" style="width: 260px" @change="onImpFormatChange">
            <el-option label="Bitwarden（CSV 导出）" value="bitwarden" />
            <el-option label="Chrome / Edge（CSV 导出）" value="chrome" />
            <el-option label="keyHive（导出 JSON，恢复备份）" value="keyhive" />
          </el-select>
        </el-form-item>
        <el-form-item :label="impFormat === 'keyhive' ? 'JSON 文件' : 'CSV 文件'">
          <input type="file" accept=".csv,.json" @change="onCsvFile" />
        </el-form-item>
      </el-form>
      <div v-if="impPreview" class="imp-preview">
        <b>预览：共 {{ impPreview.count }} 条</b>
        <div v-for="(p, i) in impPreview.preview" :key="i" class="imp-row">{{ p.title }} <span class="imp-user">（{{ p.username }}）</span></div>
      </div>
      <el-alert v-if="impFormat === 'keyhive'" type="warning" :closable="false"
        title="恢复导入：条目保持原分类与字段；必须使用明文导出的 JSON，遮蔽版（***）会被拒绝" />
      <el-alert v-else type="info" :closable="false" title="导入的条目归入 web_account 分类，密码/TOTP 标记敏感加密存储" />
      <template #footer>
        <el-button @click="impDlg = false">取消</el-button>
        <el-button :disabled="!impCsv" :loading="imping" @click="doImportPreview">预览</el-button>
        <el-button type="primary" :disabled="!impPreview" :loading="imping" @click="doImport">确认导入</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, Entry } from '../api'
import { useIsMobile } from '../ui'
import { ENTRIES_CHANGED_EVENT, me } from '../store'

const router = useRouter()
const isMobile = useIsMobile()
const entries = ref<Entry[]>([])
const categories = ref<string[]>([])
const q = ref('')
const category = ref('')
const loading = ref(false)
const staleDays = ref(90)

async function load() {
  loading.value = true
  try {
    const params = new URLSearchParams()
    if (q.value) params.set('q', q.value)
    if (category.value) params.set('category', category.value)
    entries.value = await api('/entries?' + params.toString())
    categories.value = await api('/categories')
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

// 轮换提醒阈值（设置页可改，此处动态读取）
async function loadStaleDays() {
  try { staleDays.value = (await api('/settings/stale-days')).stale_days } catch { /* 默认 90 */ }
}
function isStale(v: string): boolean {
  return daysSince(v) > staleDays.value
}

function secretCount(row: Entry) {
  return row.fields.filter((f) => f.is_secret).length
}

function open(row: Entry) {
  router.push(`/entries/${row.id}/edit`)
}

async function del(row: Entry) {
  await ElMessageBox.confirm(`确定删除「${row.title}」？此操作不可恢复`, '删除确认', { type: 'warning' })
  await api(`/entries/${row.id}`, { method: 'DELETE' })
  ElMessage.success('已删除')
  load()
}

function fmtTime(_r: any, _c: any, v: string) {
  return v ? new Date(v).toLocaleString('zh-CN') : ''
}

// 距上次更新天数
function daysSince(v: string): number {
  if (!v) return 0
  return Math.floor((Date.now() - new Date(v).getTime()) / 86400000)
}

// ---- 导出 ----
const expDlg = ref(false)
const expMode = ref('masked')
const exping = ref(false)

async function doExport() {
  exping.value = true
  try {
    let list = await api('/export')
    if (expMode.value === 'masked') {
      for (const e of list) for (const f of e.fields || []) if (f.is_secret) f.value = '***'
    }
    const blob = new Blob([JSON.stringify(list, null, 2)], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `keyhive-export-${new Date().toISOString().slice(0, 10)}${expMode.value === 'masked' ? '-masked' : ''}.json`
    a.click()
    URL.revokeObjectURL(a.href)
    ElMessage.success('导出完成（已记审计）')
    expDlg.value = false
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    exping.value = false
  }
}

// ---- 导入 ----
const impDlg = ref(false)
const impFormat = ref('bitwarden')
const impCsv = ref('')
const impPreview = ref<{ count: number; preview: { title: string; username: string }[] } | null>(null)
const imping = ref(false)

function onImpFormatChange() {
  impCsv.value = ''
  impPreview.value = null
}

function onCsvFile(ev: Event) {
  const file = (ev.target as HTMLInputElement).files?.[0]
  if (!file) { impCsv.value = ''; impPreview.value = null; return }
  const reader = new FileReader()
  reader.onload = () => { impCsv.value = String(reader.result || ''); impPreview.value = null }
  reader.readAsText(file)
}

async function doImportPreview() {
  imping.value = true
  try {
    impPreview.value = await api('/import', { method: 'POST', body: JSON.stringify({ format: impFormat.value, csv: impCsv.value, dry_run: true }) })
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    imping.value = false
  }
}

async function doImport() {
  imping.value = true
  try {
    const r = await api('/import', { method: 'POST', body: JSON.stringify({ format: impFormat.value, csv: impCsv.value, dry_run: false }) })
    ElMessage.success(`导入完成：成功 ${r.ok} 条${r.fail?.length ? `，失败 ${r.fail.length} 条` : ''}（已记审计）`)
    impDlg.value = false
    impCsv.value = ''
    impPreview.value = null
    load()
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    imping.value = false
  }
}

// AI 助手在别的组件里确认入库/更新后，这里自动刷新（window 事件，跨 chunk 可靠）
const onEntriesChanged = () => load()
window.addEventListener(ENTRIES_CHANGED_EVENT, onEntriesChanged)
onUnmounted(() => window.removeEventListener(ENTRIES_CHANGED_EVENT, onEntriesChanged))

onMounted(() => { load(); loadStaleDays() })
</script>

<style scoped>
.toolbar { display: flex; gap: 12px; margin-bottom: 16px; }
.sec { color: #e6a23c; font-size: 12px; }
.stale { color: #e6a23c; cursor: help; }
:deep(.el-table__row) { cursor: pointer; }
.imp-preview { max-height: 220px; overflow: auto; margin: 8px 0; padding: 8px; background: #f8fafc; border-radius: 4px; }
.imp-row { padding: 2px 0; }
.imp-user { color: #94a3b8; font-size: 12px; }

/* 移动端卡片 */
.cards { display: flex; flex-direction: column; gap: 10px; }
.card { cursor: pointer; }
.card :deep(.el-card__body) { padding: 12px 14px; }
.card-head { display: flex; align-items: center; gap: 6px; justify-content: space-between; }
.card-head b { word-break: break-all; }
.card-meta { margin-top: 6px; display: flex; align-items: center; gap: 8px; color: #94a3b8; font-size: 13px; }
.card-desc { margin-top: 6px; color: #64748b; font-size: 13px; overflow: hidden; text-overflow: ellipsis; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
.card-actions { margin-top: 10px; display: flex; justify-content: flex-end; gap: 8px; }

@media (max-width: 768px) {
  .toolbar { flex-wrap: wrap; }
  .toolbar .el-input, .toolbar .el-select { width: 100% !important; }
}
</style>
