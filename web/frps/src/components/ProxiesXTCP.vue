<template>
  <ProxyView :proxies="proxies" proxyType="xtcp" @refresh="fetchData"/>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { XTCPProxy } from '../utils/proxy.js'
import ProxyView from './ProxyView.vue'

let proxies = ref<XTCPProxy[]>([])

const fetchData = () => {
  fetch('../api/proxy/xtcp', { credentials: 'include' })
    .then((res) => {
      return res.json()
    })
    .then((json) => {
      proxies.value = []
      for (let proxyStats of json.proxies) {
        proxies.value.push(new XTCPProxy(proxyStats))
      }
    })
}
fetchData()
</script>

<style></style>
