import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    coverage: {
      provider: 'v8',
      include: ['src/api.ts', 'src/store.ts', 'src/ui.ts'],
      reporter: ['text', 'json-summary'],
    },
  },
})
