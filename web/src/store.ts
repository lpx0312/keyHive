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

// 条目变更通知：AI 助手等全局组件改动条目后广播，列表页监听自动刷新。
// 用 window 事件而非本模块 ref——懒加载 chunk 可能各自内联一份 store 导致双实例
export const ENTRIES_CHANGED_EVENT = 'keyhive:entries-changed'

export function notifyEntriesChanged() {
  window.dispatchEvent(new CustomEvent(ENTRIES_CHANGED_EVENT))
}
