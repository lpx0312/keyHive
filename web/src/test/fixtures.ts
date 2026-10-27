// 测试 mock 数据：与后端 API 响应结构对齐
import type { Entry, Template, APIToken, User, AuditLog } from '../api'

export const mockEntries: Entry[] = [
  {
    id: 1,
    title: '生产ACR',
    category: 'docker_registry',
    tags: ['生产', '内网'],
    description: 'CI 拉镜像用',
    ai_visible: true,
    fields: [
      { key: 'username', description: '用户名', type: 'text', is_secret: false, value: 'lipanx' },
      { key: 'password', description: '密码', type: 'text', is_secret: true, value: '***' },
    ],
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-20T00:00:00Z',
  },
  {
    id: 2,
    title: '内网Git',
    category: 'web_account',
    tags: [],
    description: '',
    ai_visible: false,
    fields: [
      { key: 'url', description: '地址', type: 'url', is_secret: false, value: 'https://git.corp' },
    ],
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-15T00:00:00Z',
  },
]

export const mockTemplates: Template[] = [
  {
    id: 1,
    category: 'docker_registry',
    name: 'Docker 镜像仓库',
    group: '容器镜像',
    fields: [
      { key: 'url', description: '仓库地址', type: 'url', is_secret: false, value: '' },
      { key: 'password', description: '密码', type: 'text', is_secret: true, value: '' },
    ],
    builtin: true,
  },
  {
    id: 2,
    category: 'my_tpl',
    name: '我的模板',
    group: '我的模板',
    fields: [],
    builtin: false,
  },
]

export const mockTokens: APIToken[] = [
  {
    id: 1,
    name: 'zcode-mcp',
    scopes: ['read', 'search', 'reveal'],
    created_at: '2026-10-01T00:00:00Z',
    last_used_at: '2026-10-26T08:00:00Z',
    expires_at: null,
    revoked_at: null,
  },
]

export const mockUsers: User[] = [
  { id: 1, username: 'admin', is_admin: true, disabled: false, created_at: '2026-01-01T00:00:00Z' },
  { id: 2, username: 'alice', is_admin: false, disabled: false, created_at: '2026-06-01T00:00:00Z' },
  { id: 3, username: 'bob', is_admin: false, disabled: true, created_at: '2026-07-01T00:00:00Z' },
]

export const mockAuditLogs: AuditLog[] = [
  {
    id: 3,
    actor_type: 'user',
    actor_id: 1,
    actor_name: 'admin',
    action: 'entry_create',
    entry_id: 1,
    detail: '{"title":"生产ACR"}',
    ip: '192.168.0.10',
    created_at: '2026-10-20T10:00:00Z',
  },
  {
    id: 2,
    actor_type: 'token',
    actor_id: 5,
    actor_name: 'zcode-mcp',
    action: 'field_reveal',
    entry_id: 1,
    detail: '{"field":"password"}',
    ip: '192.168.0.225',
    created_at: '2026-10-21T11:00:00Z',
  },
  {
    id: 1,
    actor_type: 'user',
    actor_id: 0,
    actor_name: 'hacker',
    action: 'login_failed',
    entry_id: null,
    detail: '{}',
    ip: '10.0.0.9',
    created_at: '2026-10-22T12:00:00Z',
  },
]

export const mockMe = { id: 1, username: 'admin', is_admin: true }
