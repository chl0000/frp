<template>
  <div>
    <el-page-header
    :icon="null"
    style="width: 100%; margin-left: 30px; margin-bottom: 20px"
    >
      <template #title>
        <span>fprc Clients</span>
      </template>
      <template #content> </template>
      <template #extra>
        <div class="flex items-center" style="margin-right: 30px">
          <el-button @click="handleRefresh" :loading="loading">Refresh</el-button>
        </div>
      </template>
    </el-page-header>
  </div>

  <el-table
      :data="clients"
      :default-sort="{ prop: 'name', order: 'ascending' }"
      style="width: 100%"
    >
    <el-table-column label="RunID" prop="run_id" sortable> </el-table-column>
    <el-table-column label="User" prop="user" sortable> </el-table-column>
    <el-table-column label="OS" prop="os" sortable> </el-table-column>
    <el-table-column label="Arch" prop="arch" sortable> </el-table-column>
    <el-table-column label="HostName" prop="hostname" sortable> </el-table-column>
    <el-table-column label="UUID" prop="uuid" sortable> </el-table-column>
    <el-table-column label="Privilege Key" prop="privilege_key" sortable> </el-table-column>
    <el-table-column label="Time" prop="timestamp" sortable>
      <template #default="scope">
        {{ formatTime(scope.row.timestamp) }}
      </template>
    </el-table-column>
    <el-table-column label="PoolCount" prop="pool_count" sortable>
    </el-table-column>

    <el-table-column label="ClientVersion" prop="version" sortable>
    </el-table-column>
    <el-table-column label="Operations">
      <template #default="scope">
        <el-button
          type="primary"
          :name="scope.row.name"
          style="margin-bottom: 10px"
          @click="openConfigDialog(scope.row)"
          >Config
        </el-button>
      </template>
    </el-table-column>
  </el-table>

  <el-dialog
    v-model="dialogVisible"
    destroy-on-close="true"
    :title="'RunID: ' + dialogVisibleName + ' - Config'"
    width="800px"
  >
    <el-input
      type="textarea"
      :rows="15"
      v-model="configText"
      placeholder="frpc configure..."
    ></el-input>
    <template #footer>
      <el-button @click="dialogVisible = false">Cancle</el-button>
      <el-button type="primary" @click="saveConfig" :disabled="!configModified">Update</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'

const loading = ref(false)
const dialogVisibleName = ref("")
const configText = ref("")
const configModified = ref(false) // 标记frpc的配置内容是否被修改了
const originalConfigText = ref("") // 保存原始配置内容用于比较

const dialogVisible = ref(false)
// 定义客户端数据类型
class frpc_login_info {
  version: string
  os: string
  arch: string
  uuid: string
  user: string
  hostname: string
  privilege_key: string
  timestamp: number
  run_id: string
  client_spec: any
  pool_count: number

  constructor(logininfos: any) {
    this.version = logininfos.version || ''
    this.os = logininfos.os || ''
    this.arch = logininfos.arch || ''
    this.uuid = logininfos.uuid || ''
    this.user = logininfos.user || ''
    this.hostname = logininfos.hostname || ''
    this.privilege_key = logininfos.privilege_key || ''
    this.timestamp = logininfos.timestamp || 0
    this.run_id = logininfos.run_id || ''
    this.client_spec = logininfos.client_spec || null
    this.pool_count = logininfos.pool_count || 0
  }

}

let clients = ref<frpc_login_info[]>([])

// 格式化时间戳
const formatTime = (timestamp: number) => {
  if (!timestamp) return '-'
  const date = new Date(timestamp * 1000) // 如果是秒级时间戳
  return date.toLocaleString()
}

const fetchData = () => {
  fetch('/api/get_clients', { credentials: 'include' })
    .then((res) => {
      return res.json()
    })
    .then((json) => {
      clients.value = []
      for (let logininfo of json) {
        clients.value.push(new frpc_login_info(logininfo))
      }
    })
}

onMounted(() => {
  fetchData()
})

const handleRefresh = async () => {
  loading.value = true
  try {
    await fetchData()
  } finally {
    loading.value = false
  }
}

const openConfigDialog = async (row: frpc_login_info) => {
  dialogVisibleName.value = row.run_id
  configText.value = 'loading...'  // 显示加载状态
  configModified.value = false  // 重置修改状态

  try {
    const response = await fetch(`/api/get_fprc_config?run_id=${row.run_id}`, { 
      credentials: 'include' 
    })
    
    if (response.ok) {
      const configContent = await response.text()
      configText.value = configContent
      originalConfigText.value = configContent
    } else {
      const errorText = await response.text()
      configText.value = `load fail: ${errorText}`
      console.error('load fail:', response.status, response.statusText)
    }
  } catch (error) {
    const errorMessage = error instanceof Error ? error.message : String(error)
    configText.value = `load error: ${errorMessage}`
    console.error('load error:', error)
  }
  dialogVisible.value = true
}


// 这里添加保存配置的逻辑
const saveConfig = async () => {
  const run_id = dialogVisibleName.value
  const config_content = configText.value
  
  try {
    const response = await fetch('/api/update_fprc_config', {
      method: 'PUT',
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        run_id: run_id,
        config_content: config_content
      })
    })
    
    if (response.ok) {
      console.log('update success')
      configModified.value = false  // 保存成功后重置修改状态
      dialogVisible.value = false
    } else {
      const errorText = await response.text()
      console.error('update fail:', response.status, response.statusText, errorText)
      alert(`update fail: ${errorText}`)
    }
  } catch (error) {
    const errorMessage = error instanceof Error ? error.message : String(error)
    console.error('update error:', error)
    alert(`update error: ${errorMessage}`)
  }
}

// 监听配置内容变化
watch(configText, (newValue) => {
  // 只有在原始配置已加载且内容确实发生变化时才标记为已修改
  if (originalConfigText.value !== "" && newValue !== originalConfigText.value) {
    configModified.value = true
  } else if (newValue === originalConfigText.value) {
    configModified.value = false  // 如果改回原始内容，重置状态
  }
})

</script>


<style>
.el-page-header__title {
  font-size: 20px;
}

.el-dialog__header {
  padding: 10 10px;
  height: 20px;
}

.compact-dialog .el-dialog__title {
  line-height: 20px;
  font-size: 14px;
}

.compact-dialog .el-dialog__headerbtn {
  top: 2px;
  right: 5px;
  width: 24px;
  height: 24px;
}
</style>