<template>
  <div>
    <div class="toolbar">
      <el-input v-model="q" placeholder="搜索标题 / 说明 / 分类" clearable style="width: 260px" @input="load" />
      <el-select v-model="category" placeholder="全部分类" clearable style="width: 180px" @change="load">
        <el-option v-for="c in categories" :key="c" :label="c" :value="c" />
      </el-select>
      <el-button type="primary" @click="$router.push('/entries/new')">＋ 新建条目</el-button>
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
      <el-table-column prop="updated_at" label="更新时间" width="170" :formatter="fmtTime" />
      <el-table-column label="操作" width="130" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click.stop="open(row)">编辑</el-button>
          <el-button size="small" type="danger" @click.stop="del(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, Entry } from '../api'
import { useIsMobile } from '../ui'

const router = useRouter()
const isMobile = useIsMobile()
const entries = ref<Entry[]>([])
const categories = ref<string[]>([])
const q = ref('')
const category = ref('')
const loading = ref(false)

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

onMounted(load)
</script>

<style scoped>
.toolbar { display: flex; gap: 12px; margin-bottom: 16px; }
.sec { color: #e6a23c; font-size: 12px; }
:deep(.el-table__row) { cursor: pointer; }

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
