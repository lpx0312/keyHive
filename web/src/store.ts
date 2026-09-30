import { reactive } from 'vue'
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
