<template>
  <!-- 悬浮按钮（所有登录页内可见） -->
  <div class="chat-fab" @click="open = true" title="AI 录入助手">🤖</div>

  <el-drawer v-model="open" title="🤖 AI 录入助手" size="420px" :append-to-body="true">
    <el-alert type="warning" :closable="false" class="privacy"
      title="你输入的内容（含密码）将发送到你配置的 LLM 服务商（智谱等）用于解析" />

    <div ref="listEl" class="msgs">
      <div v-for="(m, i) in messages" :key="i" :class="['msg', m.role]">
        <div class="bubble">{{ m.content }}</div>

        <!-- 草稿卡片：预览 → 确认入库 / 丢弃 -->
        <el-card v-if="m.draft && !m.done" class="draft" shadow="hover">
          <template #header>
            <el-tag v-if="m.draftKind === 'update'" type="warning" size="small" style="margin-right: 6px">更新 #{{ m.draftEntryID }}</el-tag>
            <b>{{ m.draft.title }}</b>
            <el-tag size="small" style="margin-left: 6px">{{ m.draft.category }}</el-tag>
            <el-tag v-if="!m.draft.ai_visible" size="small" type="warning" style="margin-left: 4px">AI 不可见</el-tag>
          </template>
          <div class="drow" v-for="f in m.draft.fields" :key="f.key">
            <code>{{ f.key }}</code>
            <span class="dval">{{ f.is_secret ? (f.value === '***' ? '🔒 保留库中原值' : '🔒 ' + maskLen(f.value)) : f.value }}</span>
            <small class="ddesc">{{ f.description }}</small>
          </div>
          <p v-if="m.draft.description" class="ddesc2">{{ m.draft.description }}</p>
          <div class="dbtns">
            <el-button type="primary" size="small" :loading="m.saving" @click="saveDraft(m)">
              {{ m.draftKind === 'update' ? '确认更新' : '确认入库' }}
            </el-button>
            <el-button size="small" @click="m.done = 'discarded'">丢弃</el-button>
          </div>
        </el-card>
        <el-tag v-if="m.done === 'saved'" type="success" size="small" class="donetag">✅ 已入库</el-tag>
        <el-tag v-else-if="m.done === 'discarded'" type="info" size="small" class="donetag">已丢弃</el-tag>
      </div>
    </div>

    <div class="input-area">
      <el-input v-model="text" type="textarea" :rows="3" resize="none"
        placeholder="用自然语言描述凭据，如：华为云 SWR，地址 swr.cn-east-3.xxx，用户 ci-bot，密码 abc123，组织 prod-team"
        @keydown.enter.exact.prevent="send" :disabled="loading" />
      <el-button type="primary" :loading="loading" @click="send" style="margin-top: 8px; width: 100%">
        发送（Enter）
      </el-button>
    </div>
  </el-drawer>
</template>

<script setup lang="ts">
import { ref, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import { api, Entry } from '../api'
import { notifyEntriesChanged } from '../store'

interface Msg {
  role: 'user' | 'assistant'
  content: string
  draft?: Entry
  draftKind?: 'create' | 'update'
  draftEntryID?: number
  done?: 'saved' | 'discarded'
  saving?: boolean
}

const open = ref(false)
const messages = ref<Msg[]>([
  { role: 'assistant', content: '你好！把你要存的账号/凭据信息用大白话告诉我（地址、用户名、密码、组织等），我解析成条目给你确认后入库。信息不全我会追问。' },
])
const text = ref('')
const loading = ref(false)
const listEl = ref<HTMLElement>()

function maskLen(v: string) {
  return v ? '*'.repeat(Math.min(v.length, 8)) : '(空)'
}

async function send() {
  const t = text.value.trim()
  if (!t || loading.value) return
  text.value = ''
  messages.value.push({ role: 'user', content: t })
  await scrollBottom()

  // 历史只带文本（草稿已处理的轮次不再重复带出）
  const history = messages.value
    .filter(m => !m.draft)
    .slice(-10)
    .map(m => ({ role: m.role, content: m.content }))

  loading.value = true
  try {
    const res = await api('/ai-chat', {
      method: 'POST',
      body: JSON.stringify({ messages: history.slice(0, -1), text: t }),
    })
    messages.value.push({
      role: 'assistant',
      content: res.reply || '（解析完成，请确认下方草稿）',
      draft: res.draft || undefined,
      draftKind: res.draft_kind || 'create',
      draftEntryID: res.draft_entry_id || 0,
    })
  } catch (e: any) {
    messages.value.push({ role: 'assistant', content: '❌ ' + e.message })
  } finally {
    loading.value = false
    await scrollBottom()
  }
}

async function saveDraft(m: Msg) {
  if (!m.draft) return
  m.saving = true
  try {
    // 复用现有 API：新建 POST / 更新 PUT（*** 敏感值由后端保留原值），校验 + AES 加密 + 审计全走原通道
    if (m.draftKind === 'update' && m.draftEntryID) {
      await api(`/entries/${m.draftEntryID}`, { method: 'PUT', body: JSON.stringify(m.draft) })
    } else {
      await api('/entries', { method: 'POST', body: JSON.stringify(m.draft) })
    }
    m.done = 'saved'
    notifyEntriesChanged() // 通知条目列表页自动刷新
    ElMessage.success(`${m.draftKind === 'update' ? '已更新' : '已入库'}：${m.draft.title}`)
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    m.saving = false
  }
}

async function scrollBottom() {
  await nextTick()
  if (listEl.value) listEl.value.scrollTop = listEl.value.scrollHeight
}
</script>

<style scoped>
.chat-fab {
  position: fixed; right: 24px; bottom: 24px; z-index: 100;
  width: 52px; height: 52px; border-radius: 50%;
  background: #2563eb; color: #fff; font-size: 24px;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer; box-shadow: 0 4px 12px rgba(0,0,0,.25);
}
.chat-fab:hover { background: #1d4ed8; }
.privacy { margin-bottom: 10px; }
.msgs { display: flex; flex-direction: column; gap: 10px; height: calc(100% - 150px); overflow-y: auto; padding-right: 4px; }
.msg { max-width: 92%; }
.msg.user { align-self: flex-end; }
.msg.assistant { align-self: flex-start; }
.bubble {
  padding: 8px 12px; border-radius: 10px; font-size: 14px; white-space: pre-wrap; word-break: break-all;
  background: #f1f5f9; color: #0f172a;
}
.msg.user .bubble { background: #2563eb; color: #fff; }
.draft { margin-top: 8px; max-width: 100%; }
.drow { margin: 4px 0; display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; }
.drow code { background: #e2e8f0; padding: 0 6px; border-radius: 3px; font-size: 12px; }
.dval { font-size: 13px; }
.ddesc { color: #94a3b8; font-size: 12px; flex-basis: 100%; }
.ddesc2 { color: #64748b; font-size: 12px; margin: 8px 0 0; }
.dbtns { margin-top: 10px; }
.donetag { margin-top: 6px; }
.input-area { position: absolute; bottom: 20px; left: 20px; right: 20px; }
</style>
