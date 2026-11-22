<script setup lang="ts">
import { Github } from 'lucide-vue-next'
import { t } from '../i18n'

// The release tag, baked in at build time (Dockerfile VERSION); "dev" locally.
const version = import.meta.env.VITE_APP_VERSION || 'dev'
const author = 'https://github.com/A1nar-S'
const repo = `${author}/advertisiment-monitoring`
const release = version.startsWith('v')
  ? `${repo}/releases/tag/${version}`
  : repo
const year = new Date().getFullYear()
</script>

<template>
  <footer class="border-t border-gray-150 bg-surface">
    <div
      class="mx-auto flex h-12 w-full max-w-[1200px] items-center justify-between gap-4 px-4 text-xs text-gray-500 sm:px-8 lg:px-10"
    >
      <p>
        © {{ year }}
        <a
          :href="author"
          target="_blank"
          rel="noopener"
          class="transition-colors hover:text-gray-900"
          >A1nar-S</a
        >
      </p>
      <div class="flex items-center gap-4">
        <a
          :href="release"
          target="_blank"
          rel="noopener"
          class="font-mono transition-colors hover:text-gray-900"
          >{{ version }}</a
        >
        <a
          :href="`${repo}/blob/main/LICENSE`"
          target="_blank"
          rel="noopener"
          class="transition-colors hover:text-gray-900"
          >{{ t('app.license') }}</a
        >
        <a
          :href="repo"
          target="_blank"
          rel="noopener"
          class="transition-colors hover:text-gray-900"
          :aria-label="t('app.source')"
          :title="t('app.source')"
          ><Github :size="16"
        /></a>
      </div>
    </div>
  </footer>
</template>
