<template>
  <div>
    <div class="toolbar">
      <h3>审计日志</h3>
      <el-select v-model="action" placeholder="全部动作" clearable style="width: 200px">
        <el-option v-for="a in actions" :key="a.v" :label="a.l" :value="a.v" />
      </el-select>
    </div>
    <el-table :data="logs" v-loading="loading">
      <el-table-column prop="created_at" label="时间" width="170" :formatter="fmt" />
      <el-table-column label="操作者" width="160">
        <template #default="{ row }">
          <el-tag size="small" :type="row.actor_type === 'token' ? 'warning' : 'info'">{{ row.actor_type }}</el-tag>
          {{ row.actor_name || row.actor_id }}
        </template>
      </el-table-column>
      <el-table-column label="动作" width="140">
        <template #default="{ row }">
          <el-tag size="small" :type="row.action === 'field_reveal' ? 'danger' : ''">{{ label(row.action) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="detail" label="详情" min-width="280" show-overflow-tooltip />
      <el-table-column prop="ip" label="来源 IP" width="140" />
    </el-table>

    <!-- 分页：后端 limit/offset 真分页（审计流水只增，量级会持续涨，与条目列表的本地分页不同） -->
    <el-pagination v-if="total" class="pager" v-model:current-page="page" v-model:page-size="pageSize"
      :page-sizes="[5, 10, 15, 20, 50]" :total="total" :small="isMobile"
      :layout="isMobile ? 'prev, pager, next' : 'total, sizes, prev, pager, next'" />
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { api, AuditLog } from '../api'
import { useIsMobile } from '../ui'

const isMobile = useIsMobile()
const logs = ref<AuditLog[]>([])
const action = ref('')
const loading = ref(false)

// 分页：默认 15/页，5/10/15/20/50 可选
const page = ref(1)
const pageSize = ref(15)
const total = ref(0)

const actions = [
  { v: 'login', l: '登录' },
  { v: 'login_failed', l: '登录失败' },
  { v: 'logout', l: '登出' },
  { v: 'entry_create', l: '新建条目' },
  { v: 'entry_update', l: '更新条目' },
  { v: 'entry_delete', l: '删除条目' },
  { v: 'entry_reveal', l: '人工查看敏感值' },
  { v: 'field_reveal', l: 'AI 取敏感值' },
  { v: 'token_create', l: '创建令牌' },
  { v: 'token_revoke', l: '吊销令牌' },
  { v: 'template_save', l: '存为模板' },
  { v: 'password_change', l: '修改密码' },
]

function label(a: string) {
  return actions.find((x) => x.v === a)?.l || a
}

async function load(resetPage = false) {
  loading.value = true
  try {
    if (resetPage) page.value = 1
    const params = new URLSearchParams({
      limit: String(pageSize.value),
      offset: String((page.value - 1) * pageSize.value),
      with_total: '1',
    })
    if (action.value) params.set('action', action.value)
    const r = await api('/audit?' + params.toString())
    logs.value = r.logs
    total.value = r.total
    // 筛选后总页数变少：钳到最后一页并重拉
    if (page.value > 1 && (page.value - 1) * pageSize.value >= total.value) {
      page.value = Math.max(1, Math.ceil(total.value / pageSize.value))
      return load()
    }
  } finally {
    loading.value = false
  }
}

// 翻页/改每页条数自动加载；筛选动作变化回第 1 页
watch([page, pageSize], () => load())
watch(action, () => load(true))

function fmt(_r: any, _c: any, v: string) {
  return v ? new Date(v).toLocaleString('zh-CN') : ''
}

onMounted(load)
</script>

<style scoped>
.toolbar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
.pager { margin-top: 14px; display: flex; justify-content: flex-end; }
@media (max-width: 768px) { .pager { justify-content: center; } }
</style>
