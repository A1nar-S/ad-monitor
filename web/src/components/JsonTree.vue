<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronRight } from 'lucide-vue-next'

// A JSON document as a collapsible tree. Clicking a number or string picks
// its path ("data.items[0].price"), the JSON twin of the element picker.
defineOptions({ name: 'JsonTree' })
const props = defineProps<{
  value: unknown
  path: string
  selected: string
  name?: string
  depth?: number
}>()
const emit = defineEmits<{ pick: [path: string] }>()

const depth = computed(() => props.depth ?? 0)
const open = ref(depth.value < 3 || props.selected.startsWith(props.path))

const kind = computed(() =>
  Array.isArray(props.value)
    ? 'array'
    : props.value === null
      ? 'null'
      : typeof props.value,
)
const entries = computed(() => {
  if (Array.isArray(props.value))
    return props.value.slice(0, 50).map((v, i) => ({
      key: `[${i}]`,
      value: v as unknown,
      path: `${props.path}[${i}]`,
    }))
  if (kind.value === 'object')
    return Object.entries(props.value as Record<string, unknown>).map(
      ([k, v]) => ({
        key: k,
        value: v,
        path: props.path ? `${props.path}.${k}` : k,
      }),
    )
  return []
})
const more = computed(() =>
  Array.isArray(props.value) ? Math.max(0, props.value.length - 50) : 0,
)
const pickable = computed(
  () =>
    kind.value === 'number' ||
    (kind.value === 'string' && /\d/.test(props.value as string)),
)
const shown = computed(() =>
  kind.value === 'string' ? JSON.stringify(props.value) : String(props.value),
)
const summary = computed(() =>
  kind.value === 'array'
    ? `[${(props.value as unknown[]).length}]`
    : `{${Object.keys(props.value as object).length}}`,
)
</script>

<template>
  <div class="font-mono text-xs leading-6">
    <template v-if="kind === 'object' || kind === 'array'">
      <button
        type="button"
        class="flex items-center gap-1 rounded px-1 text-gray-700 hover:bg-gray-100"
        :aria-expanded="open"
        @click="open = !open"
      >
        <ChevronRight
          :size="13"
          class="text-gray-400 transition-transform"
          :class="{ 'rotate-90': open }"
        />
        <span v-if="name !== undefined" class="text-brand-700">{{ name }}</span>
        <span class="text-gray-400">{{ summary }}</span>
      </button>
      <div v-if="open" class="ml-3 border-l border-gray-150 pl-2">
        <JsonTree
          v-for="e in entries"
          :key="e.path"
          :value="e.value"
          :path="e.path"
          :name="e.key"
          :selected="selected"
          :depth="depth + 1"
          @pick="emit('pick', $event)"
        />
        <p v-if="more" class="px-1 text-gray-400">… {{ more }}</p>
      </div>
    </template>
    <div v-else class="flex min-w-0 items-center gap-1.5 pl-5">
      <span v-if="name !== undefined" class="shrink-0 text-gray-600"
        >{{ name }}:</span
      >
      <button
        v-if="pickable"
        type="button"
        class="min-w-0 truncate rounded px-1 text-left"
        :class="
          selected === path
            ? 'bg-brand-600 text-white'
            : 'text-success-700 hover:bg-brand-50 hover:text-brand-700 hover:ring-1 hover:ring-brand-200'
        "
        :title="path"
        @click="emit('pick', path)"
      >
        {{ shown }}
      </button>
      <span v-else class="min-w-0 truncate text-gray-500">{{ shown }}</span>
    </div>
  </div>
</template>
