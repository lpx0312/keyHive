<template>
  <div>
    <h3>设置</h3>

    <el-card style="max-width: 520px">
      <template #header>修改密码</template>
      <el-form label-width="90px">
        <el-form-item label="旧密码">
          <el-input v-model="oldPw" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="newPw" type="password" show-password placeholder="至少 8 位" />
        </el-form-item>
        <el-form-item label="确认新密码">
          <el-input v-model="newPw2" type="password" show-password />
        </el-form-item>
        <el-button type="primary" @click="change">修改（修改后需重新登录）</el-button>
      </el-form>
    </el-card>

    <el-card style="max-width: 520px; margin-top: 16px">
      <template #header>
        AI 录入助手配置
        <el-tag v-if="aiCfg.configured === 'true'" type="success" size="small" style="margin-left: 8px">已配置</el-tag>
        <el-tag v-else type="info" size="small" style="margin-left: 8px">未配置</el-tag>
      </template>
      <el-form label-width="90px">
        <el-form-item label="API 地址">
          <el-input v-model="aiCfg.base_url" placeholder="OpenAI 兼容端点" />
        </el-form-item>
        <el-form-item label="API Key">
          <el-input v-model="aiCfg.api_key" show-password :placeholder="aiCfg.configured === 'true' ? '已保存（****' + tail + '），留空不修改' : 'sk-...'" />
        </el-form-item>
        <el-form-item label="模型">
          <el-input v-model="aiCfg.model" placeholder="如 glm-4.6" />
        </el-form-item>
        <el-button type="primary" :loading="savingAI" @click="saveAI">保存配置</el-button>
        <el-button :loading="testing" @click="testAI">测试连接</el-button>
        <el-alert v-if="testResult" :type="testResult.ok ? 'success' : 'error'" :closable="true"
          style="margin-top: 10px" @close="testResult = null">
          <template #title>
            <span v-if="testResult.ok">
              ✅ 连接成功（模型 {{ testResult.model }}，耗时 {{ testResult.latency_ms }}ms，回复：{{ testResult.reply || '-' }}）
            </span>
            <span v-else>❌ {{ testResult.error }}</span>
          </template>
        </el-alert>
      </el-form>
      <p class="tip">Key 用主密钥加密存储。聊天解析时，你输入的内容（含密码）会发送到该 LLM 服务商；高敏凭据建议仍用表单录入。</p>
    </el-card>

    <el-card v-if="me.isAdmin" style="max-width: 520px; margin-top: 16px">
      <template #header>密码轮换提醒</template>
      <el-form label-width="90px" inline>
        <el-form-item label="提醒阈值">
          <el-input-number v-model="staleDays" :min="7" :max="3650" />
          <span class="tip" style="margin-left: 8px">天未更新则提醒</span>
        </el-form-item>
        <el-button type="primary" :loading="savingStale" @click="saveStale">保存</el-button>
      </el-form>
      <p class="tip">条目列表中超过该天数未更新的条目会显示橙色 ⚠️ 提醒。</p>
    </el-card>

    <el-card v-if="me.isAdmin" style="max-width: 520px; margin-top: 16px">
      <template #header>
        主密钥轮换
        <el-tag type="danger" size="small" style="margin-left: 8px">高危操作</el-tag>
      </template>
      <p class="tip">用新主密钥重加密全部条目。怀疑密钥泄露时使用；操作已记审计。</p>
      <el-alert type="warning" :closable="false" style="margin-bottom: 12px"
        title="轮换后旧的 data/ 备份（库+master.key）将无法解密新库，请立即重新备份" />
      <el-button type="danger" :loading="rotating" @click="doRotate">轮换主密钥</el-button>
      <el-alert v-if="rotateResult" :type="rotateResult.new_master_key ? 'warning' : 'success'"
        :closable="false" style="margin-top: 12px">
        <template #title>
          <span v-if="rotateResult.new_master_key">
            ⚠️ 当前密钥来源是环境变量：请立即把 KEYHIVE_MASTER_KEY 更新为
            <code class="mk">{{ rotateResult.new_master_key }}</code> 并重启服务
          </span>
          <span v-else>✅ 已重加密 {{ rotateResult.entries }} 条，新密钥已写入 {{ rotateResult.key_file }}，请与数据库一起备份</span>
        </template>
      </el-alert>
    </el-card>

    <p class="version">keyHive {{ version }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api'
import { me } from '../store'

const oldPw = ref('')
const newPw = ref('')
const newPw2 = ref('')

async function change() {
  if (newPw.value.length < 8) return ElMessage.warning('新密码至少 8 位')
  if (newPw.value !== newPw2.value) return ElMessage.warning('两次输入不一致')
  await api('/password', {
    method: 'POST',
    body: JSON.stringify({ old_password: oldPw.value, new_password: newPw.value }),
  })
  ElMessage.success('密码已修改，请重新登录')
  setTimeout(() => (location.href = '/login'), 1000)
}

// ---- AI 录入助手配置 ----
const aiCfg = ref({ base_url: '', api_key: '', model: '', configured: 'false' })
const savingAI = ref(false)
const testing = ref(false)
const testResult = ref<{ ok: boolean; model?: string; latency_ms?: number; reply?: string; error?: string } | null>(null)
const tail = computed(() => aiCfg.value.api_key.slice(-4))

// 测试连接：用表单当前值（Key 留空/遮蔽时后端自动回退已保存值），无需先保存
async function testAI() {
  testing.value = true
  testResult.value = null
  try {
    testResult.value = await api('/ai-config/test', {
      method: 'POST',
      body: JSON.stringify(aiCfg.value),
    })
  } catch (e: any) {
    testResult.value = { ok: false, error: e.message }
  } finally {
    testing.value = false
  }
}

async function loadAI() {
  aiCfg.value = await api('/ai-config')
}

async function saveAI() {
  savingAI.value = true
  try {
    await api('/ai-config', { method: 'PUT', body: JSON.stringify(aiCfg.value) })
    ElMessage.success('AI 配置已保存')
    await loadAI()
    aiCfg.value.api_key = '' // 重新加载遮蔽值
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    savingAI.value = false
  }
}

// ---- 密码轮换提醒阈值 ----
const staleDays = ref(90)
const savingStale = ref(false)

async function loadStale() {
  try { staleDays.value = (await api('/settings/stale-days')).stale_days } catch { /* 默认 90 */ }
}

async function saveStale() {
  savingStale.value = true
  try {
    await api('/settings/stale-days', { method: 'PUT', body: JSON.stringify({ stale_days: staleDays.value }) })
    ElMessage.success('已保存')
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    savingStale.value = false
  }
}

// ---- 主密钥轮换 ----
const rotating = ref(false)
const rotateResult = ref<{ entries: number; key_file: string; new_master_key: string } | null>(null)

async function doRotate() {
  await ElMessageBox.confirm(
    '确定轮换主密钥？全部条目将用新密钥重加密（事务保证，失败自动回滚）。轮换后旧备份将失效。',
    '主密钥轮换确认', { type: 'warning', confirmButtonText: '确认轮换' })
  rotating.value = true
  rotateResult.value = null
  try {
    rotateResult.value = await api('/rotate-key', { method: 'POST', body: '{}' })
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    rotating.value = false
  }
}

// ---- 版本 ----
const version = ref('')
async function loadVersion() {
  try { version.value = (await api('/version')).version } catch { version.value = '' }
}

onMounted(() => { loadAI(); loadStale(); loadVersion() })
</script>

<style scoped>
.tip { color: #94a3b8; font-size: 12px; margin-top: 8px; }
.version { color: #94a3b8; font-size: 12px; margin-top: 24px; }
.mk { word-break: break-all; background: #fef0f0; padding: 0 4px; border-radius: 3px; }
</style>
