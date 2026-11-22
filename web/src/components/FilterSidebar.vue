<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { t } from '../i18n'

// The filter column of a list page (the gem finder, the admin overview):
// a card that sticks to the top of the window as the page scrolls. It is
// never taller than what is left of the window below its top, so its last
// filter is always in sight and it scrolls inside: before the page has
// scrolled it to the top, the header above leaves it less room.
defineProps<{ clearable: boolean }>()
defineEmits<{ clear: [] }>()

const gap = 24 // its sticky top-6, kept at the bottom too
const card = ref<HTMLElement>()
const maxHeight = ref<string>()

let frame = 0
function fit() {
  frame = 0
  const el = card.value
  if (!el) return
  const top = Math.max(el.getBoundingClientRect().top, gap)
  maxHeight.value = `${Math.max(window.innerHeight - top - gap, 160)}px`
}
function schedule() {
  if (!frame) frame = requestAnimationFrame(fit)
}

// Scrolling and resizing move it; so does the page above it growing (a
// status line, a row of filter chips).
const resized = new ResizeObserver(schedule)
onMounted(() => {
  fit()
  window.addEventListener('scroll', schedule, { passive: true })
  window.addEventListener('resize', schedule, { passive: true })
  resized.observe(document.body)
})
onUnmounted(() => {
  window.removeEventListener('scroll', schedule)
  window.removeEventListener('resize', schedule)
  resized.disconnect()
  cancelAnimationFrame(frame)
})
</script>

<template>
  <aside class="hidden lg:block" :aria-label="t('common.filters')">
    <div
      ref="card"
      class="card sticky top-6 overflow-y-auto overscroll-contain p-4"
      :style="{ maxHeight }"
    >
      <div class="mb-4 flex items-center justify-between">
        <h2 class="text-sm font-semibold text-gray-900">
          {{ t('common.filters') }}
        </h2>
        <button
          v-if="clearable"
          class="text-small text-brand-600 hover:underline"
          @click="$emit('clear')"
        >
          {{ t('common.clearAll') }}
        </button>
      </div>
      <slot />
    </div>
  </aside>
</template>
