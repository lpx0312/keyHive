import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    coverage: {
      provider: 'v8',
      // 全量口径：逻辑层 + 全部视图组件
      include: ['src/**/*.ts', 'src/**/*.vue'],
      // 排除：测试本身 / 类型声明 / ChatDrawer（SSE 流式解析+悬浮球拖拽+Markdown 渲染，
      // jsdom 下不可有效覆盖；其数据链路由后端 e2e（internal/api e2e + mock LLM）与浏览器实测保障）
      exclude: ['src/**/*.test.ts', 'src/test/**', 'src/env.d.ts', 'src/main.ts', 'src/components/ChatDrawer.vue'],
      reporter: ['text', 'json-summary'],
      thresholds: { lines: 90, functions: 90, statements: 90, branches: 90 },
    },
  },
})
