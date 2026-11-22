<script setup lang="ts">
import { computed } from 'vue'
import { formatNumber, t } from '../i18n'

// Numbered pages: the first and last, two either side of the current one,
// and "…" for a gap (a gap of one page shows that page instead).
const props = defineProps<{ page: number; pages: number }>()
const emit = defineEmits<{ go: [page: number] }>()

const links = computed(() => {
  const shown = new Set([1, props.pages])
  for (let n = props.page - 2; n <= props.page + 2; n++)
    if (n >= 1 && n <= props.pages) shown.add(n)
  const out: number[] = [] // 0 = a gap
  for (const n of [...shown].sort((a, b) => a - b)) {
    const prev = out[out.length - 1]
    if (prev !== undefined && n - prev === 2) out.push(n - 1)
    else if (prev !== undefined && n - prev > 2) out.push(0)
    out.push(n)
  }
  return out
})
</script>

<template>
  <nav
    v-if="pages > 1"
    class="flex flex-wrap items-center justify-center gap-1 text-small"
    :aria-label="t('common.pages')"
  >
    <button
      class="btn-subtle"
      :disabled="page <= 1"
      @click="emit('go', page - 1)"
    >
      {{ t('common.previous') }}
    </button>
    <template v-for="(n, i) in links" :key="i">
      <span v-if="n === 0" class="px-1 text-gray-400" aria-hidden="true"
        >…</span
      >
      <button
        v-else
        class="h-8 min-w-8 rounded-md px-2 tabular-nums transition-colors"
        :class="
          n === page
            ? 'bg-brand-600 font-medium text-white'
            : 'text-gray-700 hover:bg-gray-100'
        "
        :aria-current="n === page ? 'page' : undefined"
        :aria-label="t('common.pageOf', { page: n, pages })"
        @click="n !== page && emit('go', n)"
      >
        {{ formatNumber(n) }}
      </button>
    </template>
    <button
      class="btn-subtle"
      :disabled="page >= pages"
      @click="emit('go', page + 1)"
    >
      {{ t('common.next') }}
    </button>
  </nav>
</template>
