import { reactive, ref } from 'vue'
import { api } from './api'

// 全局登录态（模块级单例）
export const me = reactive<{ id: number; username: string; isAdmin: boolean; loaded: boolean }>({
  id: 0,
  username: '',
  isAdmin: false,
  loaded: false,
})

export async function loadMe() {
  try {
    const r = await api('/auth/me')
    me.id = r.id
    me.username = r.username
    me.isAdmin = !!r.is_admin
    me.loaded = true
  } catch {
    /* 401 时 api() 已跳登录 */
  }
}

// 条目数据版本号：AI 助手等全局组件改动条目后递增，列表页监听自动刷新
export const entryVersion = ref(0)

export function bumpEntries() {
  entryVersion.value++
}
