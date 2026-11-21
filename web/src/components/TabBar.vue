<script setup lang="ts" generic="T extends string">
import { formatNumber } from '../i18n'

// Underlined tabs: the overview's records, the editor's sites. The parent
// owns the selection (v-model) and what each tab shows.
export type Tab<T> = { id: T; label: string; count?: number }

defineProps<{ tabs: readonly Tab<T>[]; label: string }>()
const model = defineModel<T>({ required: true })
</script>

<template>
  <div
    class="flex gap-6 border-b border-gray-200"
    role="tablist"
    :aria-label="label"
  >
    <button
      v-for="tab in tabs"
      :key="tab.id"
      type="button"
      role="tab"
      :aria-selected="model === tab.id"
      class="relative -mb-px flex h-10 items-center gap-2 text-sm transition-colors"
      :class="
        model === tab.id
          ? 'font-medium text-gray-900'
          : 'text-gray-600 hover:text-gray-900'
      "
      @click="model = tab.id"
    >
      {{ tab.label }}
      <span
        v-if="tab.count !== undefined"
        class="text-small font-normal text-gray-500 tabular-nums"
        >{{ formatNumber(tab.count) }}</span
      >
      <span
        v-if="model === tab.id"
        class="absolute inset-x-0 bottom-0 h-[3px] rounded-full bg-brand-600"
        aria-hidden="true"
      />
    </button>
  </div>
</template>
