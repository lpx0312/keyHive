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
      </el-form>
      <p class="tip">Key 用主密钥加密存储。聊天解析时，你输入的内容（含密码）会发送到该 LLM 服务商；高敏凭据建议仍用表单录入。</p>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../api'

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
const tail = computed(() => aiCfg.value.api_key.slice(-4))

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

onMounted(loadAI)
</script>

<style scoped>
.tip { color: #94a3b8; font-size: 12px; margin-top: 8px; }
</style>
