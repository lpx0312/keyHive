// API 封装：统一错误处理与 401 跳转
export async function api(path: string, opts: RequestInit = {}): Promise<any> {
  const res = await fetch('/api/v1' + path, {
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    ...opts,
  })
  if (res.status === 401 && location.pathname !== '/login') {
    location.href = '/login'
    throw new Error('未登录')
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error(data.error || `请求失败 (${res.status})`)
  }
  return data
}

export interface Field {
  key: string
  description: string
  type: string
  is_secret: boolean
  value: string
}

export interface Entry {
  id: number
  title: string
  category: string
  tags?: string[]
  description: string
  ai_visible: boolean
  fields: Field[]
  created_at: string
  updated_at: string
}

export interface Template {
  id: number
  category: string
  name: string
  group: string
  fields: Field[]
  builtin: boolean
}

export interface APIToken {
  id: number
  name: string
  scopes: string[]
  created_at: string
  last_used_at: string | null
  expires_at: string | null
  revoked_at: string | null
  token?: string
}

export interface User {
  id: number
  username: string
  is_admin: boolean
  disabled: boolean
  created_at: string
}

export interface AuditLog {
  id: number
  actor_type: string
  actor_id: number
  actor_name: string
  action: string
  entry_id: number | null
  detail: string
  ip: string
  created_at: string
}
