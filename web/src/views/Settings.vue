<template>
  <div>
    <div class="page-head">
      <div class="page-title">
        <h3>设置</h3>
        <p class="page-tip">账号密码、AI 录入助手、轮换提醒与主密钥管理。</p>
      </div>
    </div>

    <el-card class="sec-card">
      <template #header><span class="sec-title">修改密码</span></template>
      <el-form label-width="90px" class="sec-form">
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

    <el-card class="sec-card">
      <template #header><span class="sec-title">界面</span></template>
      <el-form label-width="120px" class="sec-form">
        <el-form-item label="AI 助手悬浮球">
          <el-switch v-model="fabVisible" @change="onFabVisibleChange" />
        </el-form-item>
      </el-form>
      <p class="tip">悬浮球可拖动到任意位置避开遮挡；在悬浮球上长按或右键也可直接隐藏（隐藏后在此重新开启）。</p>
    </el-card>

    <el-card class="sec-card">
      <template #header>
        <div class="head-row">
          <span class="sec-title">AI 录入助手配置</span>
          <el-tag v-if="aiCfg.configured === 'true'" type="success" size="small" effect="light" round>已配置</el-tag>
          <el-tag v-else type="info" size="small" effect="light" round>未配置</el-tag>
        </div>
      </template>
      <el-form label-width="90px" class="sec-form">
        <el-form-item label="协议">
          <el-radio-group v-model="aiCfg.protocol" @change="onProtocolChange">
            <el-radio value="openai">OpenAI 兼容</el-radio>
            <el-radio value="anthropic">Anthropic</el-radio>
          </el-radio-group>
          <p class="tip" style="margin-top: 2px">绝大多数服务商（含国内）都兼容 OpenAI 协议；Claude 系走 Anthropic 协议</p>
        </el-form-item>
        <el-form-item label="服务商预设">
          <el-select v-model="presetSel" placeholder="选择服务商，自动填地址和常用模型" clearable
            style="width: 100%" @change="onPreset">
            <el-option v-for="p in presets" :key="p.name" :label="p.name" :value="p.name">
              <span>{{ p.name }}</span>
              <span class="pbadge" :class="p.protocol">{{ p.protocol === 'anthropic' ? 'Anthropic' : 'OpenAI' }}</span>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item label="API 地址">
          <el-input v-model="aiCfg.base_url" :placeholder="curProtocol === 'anthropic' ? '如 https://api.anthropic.com' : 'OpenAI 兼容端点，如 https://api.deepseek.com/v1'" />
        </el-form-item>
        <el-form-item label="API Key">
          <el-input v-model="aiCfg.api_key" show-password :placeholder="keyPlaceholder" />
        </el-form-item>
        <el-form-item label="模型">
          <div class="model-row">
            <el-select v-model="aiCfg.model" filterable allow-create default-first-option
              placeholder="下拉选择，或直接输入自定义模型名" style="flex: 1">
              <el-option v-for="m in modelOptions" :key="m" :label="m" :value="m" />
            </el-select>
            <el-button :loading="fetchingModels" @click="fetchModels">获取模型列表</el-button>
          </div>
          <p class="tip" style="margin-top: 2px">
            从服务商拉取全部可用模型（用已填地址和 Key）；预设自带的常用模型无需拉取即可选
          </p>
        </el-form-item>
        <el-button type="primary" :loading="savingAI" @click="saveAI">保存配置</el-button>
        <el-button :loading="testing" @click="testAI">测试连接</el-button>
        <el-alert v-if="testResult" :type="testResult.ok ? 'success' : 'error'" :closable="true"
          style="margin-top: 10px" @close="testResult = null">
          <template #title>
            <span v-if="testResult.ok" class="tr-ok"><el-icon><CircleCheckFilled /></el-icon>连接成功{{ latencyText }}</span>
            <span v-else class="tr-fail"><el-icon><CircleCloseFilled /></el-icon>连接失败{{ latencyText }}</span>
          </template>
          <div v-if="testResult.ok" class="test-detail">
            <div>{{ protocolLabel }} 协议 · 模型 {{ testResult.model || '-' }}</div>
            <div v-if="testResult.reply" class="test-reply">回复：{{ testResult.reply }}</div>
          </div>
          <div v-else class="test-detail">{{ testResult.error || '未知错误' }}</div>
        </el-alert>
      </el-form>
      <p class="tip">Key 用主密钥加密存储。聊天解析时，你输入的内容（含密码）会发送到该 LLM 服务商；高敏凭据建议仍用表单录入。</p>
    </el-card>

    <el-card v-if="me.isAdmin" class="sec-card">
      <template #header><span class="sec-title">密码轮换提醒</span></template>
      <el-form label-width="90px" inline class="sec-form">
        <el-form-item label="提醒阈值">
          <el-input-number v-model="staleDays" :min="7" :max="3650" />
          <span class="tip" style="margin-left: 8px">天未更新则提醒</span>
        </el-form-item>
        <el-button type="primary" :loading="savingStale" @click="saveStale">保存</el-button>
      </el-form>
      <p class="tip">条目列表中超过该天数未更新的条目会显示橙色 ⚠️ 提醒。</p>
    </el-card>

    <el-card v-if="me.isAdmin" class="sec-card">
      <template #header>
        <div class="head-row">
          <span class="sec-title">主密钥轮换</span>
          <el-tag type="danger" size="small" effect="light" round>高危操作</el-tag>
        </div>
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
import { CircleCheckFilled, CircleCloseFilled } from '@element-plus/icons-vue'
import { api } from '../api'
import { me, setFabHidden } from '../store'

const oldPw = ref('')
const newPw = ref('')
const newPw2 = ref('')

// ---- AI 助手悬浮球开关（隐藏入口在悬浮球长按/右键，恢复入口在此） ----
const fabVisible = ref(localStorage.getItem('kh-fab-hidden') !== '1')
function onFabVisibleChange(v: any) {
  setFabHidden(!v) // 写 localStorage 并广播事件，ChatDrawer 跨 chunk 同步
}

async function change() {
  if (newPw.value.length < 8) return ElMessage.warning('新密码至少 8 位')
  if (newPw.value !== newPw2.value) return ElMessage.warning('两次输入不一致')
  try {
    await api('/password', {
      method: 'POST',
      body: JSON.stringify({ old_password: oldPw.value, new_password: newPw.value }),
    })
    ElMessage.success('密码已修改，请重新登录')
    setTimeout(() => (location.href = '/login'), 1000)
  } catch (e: any) {
    // 旧密码错误等失败路径：明确反馈错误信息，避免点击后毫无反应（审查 P2）
    ElMessage.error(e.message)
  }
}

// ---- AI 录入助手配置 ----
interface AIPreset { name: string; protocol: string; base_url: string; models: string[]; key_hint?: string }
const aiCfg = ref<any>({ protocol: 'openai', base_url: '', api_key: '', model: '', configured: 'false' })
const presets = ref<AIPreset[]>([])
const presetSel = ref('') // 服务商预设选择；空 = 手动自定义
const fetchedModels = ref<string[]>([]) // 「获取模型列表」拉到的全量模型
const fetchingModels = ref(false)
const savingAI = ref(false)
const testing = ref(false)
const testResult = ref<{ ok: boolean; model?: string; protocol?: string; latency_ms?: number; reply?: string; error?: string } | null>(null)
// 耗时与协议标签：后端未返回时优雅缺省（不显示"耗时 ms"空壳）
const latencyText = computed(() =>
  typeof testResult.value?.latency_ms === 'number' ? `（耗时 ${testResult.value.latency_ms}ms）` : '')
const protocolLabel = computed(() => (testResult.value?.protocol === 'anthropic' ? 'Anthropic' : 'OpenAI 兼容'))
const tail = computed(() => (aiCfg.value.api_key || '').slice(-4))

const curProtocol = computed(() => (aiCfg.value.protocol === 'anthropic' ? 'anthropic' : 'openai'))
const curPreset = computed(() => presets.value.find(p => p.name === presetSel.value))

// Key 输入框提示：已保存则提示留空不修改，否则给预设的 Key 获取地提示
const keyPlaceholder = computed(() => {
  const hint = curPreset.value?.key_hint || ''
  if (aiCfg.value.configured === 'true')
    return `已保存（****${tail.value}），留空不修改${hint ? '；' + hint : ''}`
  return hint || 'sk-...'
})

// 模型下拉候选：预设常用模型 + 已拉取的全量列表 + 当前值兜底（去重）
const modelOptions = computed(() => {
  const set = new Set<string>([...(curPreset.value?.models || []), ...fetchedModels.value])
  if (aiCfg.value.model) set.add(aiCfg.value.model)
  return Array.from(set)
})

// 切协议时清掉按旧协议拉的模型清单，避免误导
function onProtocolChange() {
  fetchedModels.value = []
}

// 选中预设：自动填协议 / 地址 / 常用模型与 Key 提示
function onPreset(name: string) {
  const p = presets.value.find(p => p.name === name)
  if (!p) return
  aiCfg.value.protocol = p.protocol === 'anthropic' ? 'anthropic' : 'openai'
  aiCfg.value.base_url = p.base_url
  fetchedModels.value = []
  const ms = p.models || []
  if (ms.length && !ms.includes(aiCfg.value.model)) aiCfg.value.model = ms[0]
}

// 获取模型列表：用表单当前值请求（Key 留空/遮蔽时后端回退已保存值）
async function fetchModels() {
  fetchingModels.value = true
  try {
    const r = await api('/ai-config/models', {
      method: 'POST',
      body: JSON.stringify({
        protocol: aiCfg.value.protocol, base_url: aiCfg.value.base_url, api_key: aiCfg.value.api_key,
      }),
    })
    if (!r.ok) throw new Error(r.error || '获取失败')
    fetchedModels.value = r.models || []
    if (!fetchedModels.value.length) throw new Error('服务商未返回任何模型')
    if (!fetchedModels.value.includes(aiCfg.value.model)) aiCfg.value.model = fetchedModels.value[0]
    ElMessage.success(`已获取 ${fetchedModels.value.length} 个模型`)
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    fetchingModels.value = false
  }
}

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
  const r = await api('/ai-config')
  presets.value = r.presets || []
  delete r.presets
  aiCfg.value = { ...r, protocol: r.protocol === 'anthropic' ? 'anthropic' : 'openai' }
  // 已保存地址+协议与某个预设匹配时回显预设名
  const p = presets.value.find(p => p.base_url === aiCfg.value.base_url && p.protocol === aiCfg.value.protocol)
  presetSel.value = p ? p.name : ''
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
.sec-card { max-width: 560px; margin-bottom: 16px; border-radius: 10px; border-color: #e6eaf2; }
.sec-card :deep(.el-card__header) { padding: 12px 16px; }
.sec-title { font-size: 14px; font-weight: 600; color: #1d2939; }
.head-row { display: flex; align-items: center; gap: 8px; }
.sec-form { max-width: 460px; }
.tr-ok { display: inline-flex; align-items: center; gap: 4px; }
.tr-ok .el-icon { color: #67c23a; }
.tr-fail { display: inline-flex; align-items: center; gap: 4px; }
.tr-fail .el-icon { color: #f56c6c; }
.tip { color: #94a3b8; font-size: 12px; margin-top: 8px; }
.version { color: #94a3b8; font-size: 12px; margin-top: 24px; }
.mk { word-break: break-all; background: #fef0f0; padding: 0 4px; border-radius: 3px; }
.model-row { display: flex; gap: 8px; width: 100%; }
.pbadge { float: right; font-size: 11px; color: #10b981; }
.pbadge.anthropic { color: #d97706; }
.test-detail { font-size: 12px; line-height: 1.7; }
.test-reply { color: #64748b; word-break: break-all; }
</style>
