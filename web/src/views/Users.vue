<template>
  <div>
    <div class="toolbar">
      <h3>用户管理</h3>
      <el-button type="primary" @click="dlg = true">＋ 新建用户</el-button>
    </div>
    <el-alert type="info" :closable="false" style="margin-bottom: 16px"
      title="所有用户共享同一个密钥库；普通用户可管理条目与模板，用户管理 / AI 令牌 / 审计日志仅管理员可用。" />

    <el-table :data="users" v-loading="loading">
      <el-table-column prop="username" label="用户名" min-width="140">
        <template #default="{ row }">
          <b>{{ row.username }}</b>
          <el-tag v-if="row.is_admin" size="small" type="danger" style="margin-left: 6px">管理员</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag v-if="row.disabled" type="info" size="small">已禁用</el-tag>
          <el-tag v-else type="success" size="small">正常</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" width="170" :formatter="fmt" />
      <el-table-column label="操作" width="320" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="resetPW(row)">重置密码</el-button>
          <el-button size="small" :type="row.disabled ? 'success' : 'warning'" @click="toggle(row)">
            {{ row.disabled ? '启用' : '禁用' }}
          </el-button>
          <el-button size="small" @click="toggleAdmin(row)">{{ row.is_admin ? '降为普通' : '设为管理员' }}</el-button>
          <el-button size="small" type="danger" @click="del(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dlg" title="新建用户" width="420px">
      <el-form label-width="80px">
        <el-form-item label="用户名">
          <el-input v-model="form.username" placeholder="登录用户名" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="form.password" type="password" show-password placeholder="至少 8 位" />
        </el-form-item>
        <el-form-item label="角色">
          <el-checkbox v-model="form.is_admin">管理员（可管理用户 / AI 令牌 / 审计）</el-checkbox>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dlg = false">取消</el-button>
        <el-button type="primary" @click="create">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, User } from '../api'

const users = ref<User[]>([])
const loading = ref(false)
const dlg = ref(false)
const form = ref({ username: '', password: '', is_admin: false })

async function load() {
  loading.value = true
  try {
    users.value = await api('/users')
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

async function create() {
  if (!form.value.username.trim()) return ElMessage.warning('请填写用户名')
  if (form.value.password.length < 8) return ElMessage.warning('密码至少 8 位')
  try {
    await api('/users', { method: 'POST', body: JSON.stringify(form.value) })
    ElMessage.success('已创建')
    dlg.value = false
    form.value = { username: '', password: '', is_admin: false }
    load()
  } catch (e: any) {
    ElMessage.error(e.message)
  }
}

async function resetPW(row: User) {
  const { value } = await ElMessageBox.prompt(`为「${row.username}」设置新密码（至少 8 位，其所有会话将被踢出）`, '重置密码', {
    inputType: 'password',
    inputPattern: /^.{8,}$/,
    inputErrorMessage: '密码至少 8 位',
  })
  await api(`/users/${row.id}`, { method: 'PUT', body: JSON.stringify({ password: value }) })
  ElMessage.success('密码已重置')
}

async function toggle(row: User) {
  await api(`/users/${row.id}`, { method: 'PUT', body: JSON.stringify({ disabled: !row.disabled }) })
  ElMessage.success(row.disabled ? '已启用' : '已禁用并踢出其会话')
  load()
}

async function toggleAdmin(row: User) {
  await api(`/users/${row.id}`, { method: 'PUT', body: JSON.stringify({ is_admin: !row.is_admin }) })
  ElMessage.success(row.is_admin ? '已降为普通用户' : '已设为管理员')
  load()
}

async function del(row: User) {
  await ElMessageBox.confirm(`确定删除用户「${row.username}」？`, '删除确认', { type: 'warning' })
  await api(`/users/${row.id}`, { method: 'DELETE' })
  ElMessage.success('已删除')
  load()
}

function fmt(_r: any, _c: any, v: string) {
  return v ? new Date(v).toLocaleString('zh-CN') : ''
}

onMounted(load)
</script>

<style scoped>
.toolbar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
</style>
