<template>
  <div>
    <h3>设置</h3>
    <el-card style="max-width: 480px">
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
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
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
</script>
