<template>
  <div>
    <h3>录入模板</h3>
    <p class="tip">内置模板覆盖常见运维场景；在条目编辑页「另存为模板」可沉淀自己的结构。</p>
    <el-collapse v-model="open">
      <el-collapse-item v-for="g in groups" :key="g.name" :name="g.name" :title="`${g.name}（${g.items.length}）`">
        <el-table :data="g.items" size="small">
          <el-table-column prop="name" label="模板名" width="200">
            <template #default="{ row }">
              {{ row.name }}
              <el-tag v-if="!row.builtin" size="small" type="success">自建</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="category" label="标识" width="160" />
          <el-table-column label="字段骨架（名称 → 给 AI 的说明）">
            <template #default="{ row }">
              <div v-for="f in row.fields" :key="f.key" class="f">
                <code>{{ f.key }}</code>
                <span class="desc">{{ f.description }}</span>
                <el-tag v-if="f.is_secret" size="small" type="warning">敏感</el-tag>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="80" v-if="true">
            <template #default="{ row }">
              <el-button v-if="!row.builtin" size="small" type="danger" @click="del(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-collapse-item>
    </el-collapse>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { api, Template } from '../api'

const templates = ref<Template[]>([])
const open = ref<string[]>([])

const groups = computed(() => {
  const m: Record<string, Template[]> = {}
  for (const t of templates.value) (m[t.group] ||= []).push(t)
  return Object.entries(m).map(([name, items]) => ({ name, items }))
})

async function load() {
  templates.value = await api('/templates')
}

async function del(row: Template) {
  await api(`/templates/${row.id}`, { method: 'DELETE' })
  ElMessage.success('已删除')
  load()
}

onMounted(load)
</script>

<style scoped>
.tip { color: #94a3b8; font-size: 13px; }
.f { margin: 2px 0; }
.f code { background: #f1f5f9; padding: 1px 6px; border-radius: 3px; margin-right: 8px; }
.f .desc { color: #64748b; margin-right: 6px; }
</style>
