<template>
  <div>
    <div class="toolbar">
      <h3>AI 访问令牌</h3>
      <el-button type="primary" @click="dlg = true">＋ 新建令牌</el-button>
    </div>
    <el-alert type="info" :closable="false" style="margin-bottom: 16px"
      title="令牌明文只在创建时显示一次。read=可读条目结构与注释（敏感值遮蔽）；search=可搜索；reveal=可取敏感字段明文（每次取用均记审计）。" />

    <el-table :data="tokens">
      <el-table-column prop="name" label="名称" width="160" />
      <el-table-column label="权限" width="220">
        <template #default="{ row }">
          <el-tag v-for="s in row.scopes" :key="s" size="small" :type="s === 'reveal' ? 'danger' : 'info'" style="margin-right: 4px">{{ s }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" width="170" :formatter="fmt" />
      <el-table-column prop="last_used_at" label="最后使用" width="170" :formatter="fmt" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag v-if="row.revoked_at" type="info" size="small">已吊销</el-tag>
          <el-tag v-else type="success" size="small">有效</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="100">
        <template #default="{ row }">
          <el-button v-if="!row.revoked_at" size="small" type="danger" @click="revoke(row)">吊销</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dlg" title="新建 AI 令牌" width="460px">
      <el-form label-width="80px">
        <el-form-item label="名称">
          <el-input v-model="name" placeholder="如 cursor-mcp、claude-skill" />
        </el-form-item>
        <el-form-item label="权限">
          <el-checkbox v-model="sc.read" disabled>read（基础，必选）</el-checkbox><br />
          <el-checkbox v-model="sc.search">search（允许搜索）</el-checkbox><br />
          <el-checkbox v-model="sc.reveal">reveal（允许取敏感值明文 ⚠️ 每次取用记审计）</el-checkbox>
        </el-form-item>
        <el-form-item label="过期时间">
          <el-date-picker v-model="expires" type="datetime" placeholder="留空 = 永不过期" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dlg = false">取消</el-button>
        <el-button type="primary" @click="create">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showToken" title="令牌已创建（仅此一次显示）" width="560px">
      <el-input :model-value="plaintext" readonly>
        <template #append>
          <el-button @click="copy">复制</el-button>
        </template>
      </el-input>
      <p class="warn">请立即复制保存。关闭后无法再次查看，丢失只能吊销重建。</p>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, APIToken } from '../api'

const tokens = ref<APIToken[]>([])
const dlg = ref(false)
const name = ref('')
const sc = ref({ read: true, search: true, reveal: false })
const expires = ref<Date | null>(null)
const showToken = ref(false)
const plaintext = ref('')

async function load() {
  tokens.value = await api('/tokens')
}

async function create() {
  if (!name.value.trim()) return ElMessage.warning('请填写名称')
  const scopes = ['read']
  if (sc.value.search) scopes.push('search')
  if (sc.value.reveal) scopes.push('reveal')
  const body: any = { name: name.value.trim(), scopes }
  if (expires.value) body.expires_at = new Date(expires.value).toISOString()
  const t: APIToken = await api('/tokens', { method: 'POST', body: JSON.stringify(body) })
  plaintext.value = t.token!
  showToken.value = true
  dlg.value = false
  name.value = ''
  load()
}

async function revoke(row: APIToken) {
  await ElMessageBox.confirm(`确定吊销「${row.name}」？使用该令牌的 AI 将立即无法访问`, '吊销确认', { type: 'warning' })
  await api(`/tokens/${row.id}`, { method: 'DELETE' })
  ElMessage.success('已吊销')
  load()
}

function copy() {
  navigator.clipboard.writeText(plaintext.value)
  ElMessage.success('已复制')
}

function fmt(_r: any, _c: any, v: string | null) {
  return v ? new Date(v).toLocaleString('zh-CN') : '—'
}

onMounted(load)
</script>

<style scoped>
.toolbar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
.warn { color: #f56c6c; font-size: 13px; }
</style>
