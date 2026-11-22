<script setup lang="ts">
import { computed } from 'vue'
import { t } from '../i18n'

// The release tag, baked in at build time (Dockerfile VERSION); "dev" locally.
const version = import.meta.env.VITE_APP_VERSION || 'dev'
const author = 'https://github.com/A1nar-S'
const repo = `${author}/advertisiment-monitoring`
const year = new Date().getFullYear()
const links = computed(() => [
  {
    label: version,
    href: version.startsWith('v') ? `${repo}/releases/tag/${version}` : repo,
  },
  { label: t('app.license'), href: `${repo}/blob/main/LICENSE` },
  { label: t('app.source'), href: repo },
])
</script>

<template>
  <footer
    class="flex flex-wrap items-center justify-center gap-x-2 gap-y-1 text-xs text-gray-500"
  >
    <span
      >© {{ year }}
      <a
        :href="author"
        target="_blank"
        rel="noopener"
        class="underline-offset-2 hover:text-gray-900 hover:underline"
        >A1nar-S</a
      ></span
    >
    <template v-for="link in links" :key="link.label">
      <span aria-hidden="true">·</span>
      <a
        :href="link.href"
        target="_blank"
        rel="noopener"
        class="underline-offset-2 hover:text-gray-900 hover:underline"
        >{{ link.label }}</a
      >
    </template>
  </footer>
</template>
