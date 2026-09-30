<template>
  <div>
    <div class="toolbar">
      <h3>审计日志</h3>
      <el-select v-model="action" placeholder="全部动作" clearable style="width: 200px" @change="load">
        <el-option v-for="a in actions" :key="a.v" :label="a.l" :value="a.v" />
      </el-select>
    </div>
    <el-table :data="logs">
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
    <el-button style="margin-top: 12px" :disabled="logs.length < 50" @click="more">加载更多</el-button>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { api, AuditLog } from '../api'

const logs = ref<AuditLog[]>([])
const action = ref('')
const offset = ref(0)

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

async function load() {
  offset.value = 0
  const params = new URLSearchParams({ limit: '50' })
  if (action.value) params.set('action', action.value)
  logs.value = await api('/audit?' + params.toString())
}

async function more() {
  offset.value += 50
  const params = new URLSearchParams({ limit: '50', offset: String(offset.value) })
  if (action.value) params.set('action', action.value)
  logs.value.push(...(await api('/audit?' + params.toString())))
}

function fmt(_r: any, _c: any, v: string) {
  return v ? new Date(v).toLocaleString('zh-CN') : ''
}

onMounted(load)
</script>

<style scoped>
.toolbar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
</style>
