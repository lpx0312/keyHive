<template>
  <div class="wrap">
    <el-card class="card">
      <h2>🍯 keyHive 密钥管家</h2>
      <el-form @submit.prevent="login">
        <el-form-item>
          <el-input v-model="username" placeholder="用户名" size="large" autofocus />
        </el-form-item>
        <el-form-item>
          <el-input v-model="password" type="password" placeholder="密码" size="large" show-password />
        </el-form-item>
        <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="login">
          登 录
        </el-button>
      </el-form>
      <p class="tip">首次启动的初始密码见服务端启动日志</p>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../api'

const username = ref('')
const password = ref('')
const loading = ref(false)

async function login() {
  if (!username.value || !password.value) return
  loading.value = true
  try {
    await api('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: username.value, password: password.value }),
    })
    location.href = '/entries'
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.wrap { min-height: 100vh; display: flex; align-items: center; justify-content: center; background: #1d2939; }
.card { width: min(360px, 92vw); }
.tip { color: #94a3b8; font-size: 12px; text-align: center; }
</style>
