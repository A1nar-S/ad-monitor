<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { CircleAlert, CircleCheck, Info, X } from 'lucide-vue-next'
import { dismiss, hold, release, toasts } from '../toast'
import { t } from '../i18n'

const icons = { success: CircleCheck, error: CircleAlert, info: Info }
const tones = {
  success: 'text-success-600',
  error: 'text-danger-600',
  info: 'text-brand-600',
}

// The stack lives in the top layer (a manual popover), so a toast also shows
// over an open <dialog>. Showing it again moves it above a dialog opened
// since; browsers without popovers fall back to a plain fixed layer.
const stack = ref<HTMLElement>()
function toFront() {
  const el = stack.value
  if (!el || !('showPopover' in el)) return
  try {
    if (el.matches(':popover-open')) el.hidePopover()
    el.showPopover()
  } catch {
    // Not connected yet, or popovers unsupported after all.
  }
}
onMounted(toFront)
watch(
  () => toasts.value.length,
  (n, before) => {
    if (n > before) toFront()
  },
)

// Only a mouse resting on a toast holds it; a tap must not pin it forever.
const onEnter = (e: PointerEvent, id: number) => {
  if (e.pointerType === 'mouse') hold(id)
}
const onLeave = (e: PointerEvent, id: number) => {
  if (e.pointerType === 'mouse') release(id)
}
</script>

<template>
  <!-- Above the editors' save bar. Each toast announces itself once: a
       status politely, an error at once. -->
  <div
    ref="stack"
    popover="manual"
    class="pointer-events-none fixed inset-x-4 top-auto bottom-20 z-50 m-0 h-auto w-auto flex flex-col items-center gap-2 overflow-visible border-0 bg-transparent p-0 sm:inset-x-auto sm:right-6 sm:left-auto sm:items-end"
  >
    <TransitionGroup name="toast">
      <div
        v-for="item in toasts"
        :key="item.id"
        :role="item.kind === 'error' ? 'alert' : 'status'"
        class="pointer-events-auto flex w-full max-w-sm items-start gap-2.5 rounded-lg bg-raised py-2.5 pr-2 pl-3 text-sm text-gray-900 shadow-overlay ring-1 ring-gray-150"
        @pointerenter="onEnter($event, item.id)"
        @pointerleave="onLeave($event, item.id)"
      >
        <component
          :is="icons[item.kind]"
          :size="16"
          class="mt-0.5 shrink-0"
          :class="tones[item.kind]"
        />
        <span class="min-w-0 flex-1 [overflow-wrap:anywhere]">{{
          item.message
        }}</span>
        <button
          class="grid size-6 shrink-0 place-items-center rounded text-gray-500 hover:bg-gray-100 hover:text-gray-900"
          :aria-label="t('common.close')"
          :title="t('common.close')"
          @click="dismiss(item.id)"
        >
          <X :size="14" />
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.toast-enter-active,
.toast-leave-active {
  transition:
    opacity 180ms ease,
    transform 180ms ease;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(8px);
}
@media (prefers-reduced-motion: reduce) {
  .toast-enter-active,
  .toast-leave-active {
    transition: opacity 120ms ease;
  }
  .toast-enter-from,
  .toast-leave-to {
    transform: none;
  }
}
</style>
