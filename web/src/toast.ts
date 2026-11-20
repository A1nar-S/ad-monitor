import { ref } from 'vue'

// Short notes about what just happened ("Search saved"), shown by ToastHost
// in the corner. They outlive a page change, so an editor can say "saved"
// and go back to the list.
export type Toast = {
  id: number
  kind: 'success' | 'error' | 'info'
  message: string
}

export const toasts = ref<Toast[]>([])

const lifetime = { success: 3500, info: 3500, error: 7000 }
const maxShown = 3
let next = 1
const timers = new Map<number, ReturnType<typeof setTimeout>>()

export function toast(message: string, kind: Toast['kind'] = 'success') {
  const id = next++
  const all = [...toasts.value, { id, kind, message }]
  for (const old of all.slice(0, -maxShown)) {
    clearTimeout(timers.get(old.id))
    timers.delete(old.id)
  }
  toasts.value = all.slice(-maxShown)
  timers.set(
    id,
    setTimeout(() => dismiss(id), lifetime[kind]),
  )
  return id
}

export const toastError = (message: string) => toast(message, 'error')

export function dismiss(id: number) {
  clearTimeout(timers.get(id))
  timers.delete(id)
  toasts.value = toasts.value.filter((t) => t.id !== id)
}

// Hovering keeps a toast up; leaving restarts its clock.
export function hold(id: number) {
  clearTimeout(timers.get(id))
}
export function release(id: number) {
  const t = toasts.value.find((x) => x.id === id)
  if (t)
    timers.set(
      id,
      setTimeout(() => dismiss(id), lifetime[t.kind]),
    )
}
