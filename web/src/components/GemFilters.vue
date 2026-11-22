<script setup lang="ts">
import { computed } from 'vue'
import { Info } from 'lucide-vue-next'
import MultiSelect from './MultiSelect.vue'
import ToggleSwitch from './ToggleSwitch.vue'
import type { GemBuy, GemCount, GemPage, GemQuery } from '../api'
import { formatNumber, t, tKey } from '../i18n'

// The gem finder's filters. The parent owns the query (v-model) and loads
// the cars when it changes; facets say which values there are, with counts.
const props = defineProps<{
  facets: GemPage['facets'] | null
  detailed: number
  all: number
}>()
const query = defineModel<GemQuery>({ required: true })

const buys: GemBuy[] = ['good', 'average', 'bad', 'check']
const bodyOrder = [
  'sedan',
  'wagon',
  'hatchback',
  'suv',
  'coupe',
  'convertible',
  'minivan',
  'minibus',
  'pickup',
]

const makes = computed(() => {
  const counts = new Map<string, number>()
  for (const m of props.facets?.models ?? [])
    counts.set(m.make, (counts.get(m.make) ?? 0) + m.count)
  return [...counts].map(([make, count]) => ({ make, count }))
})
const models = computed(() =>
  (props.facets?.models ?? []).filter((m) => m.make === query.value.make),
)
const bodies = computed(() =>
  [...(props.facets?.bodies ?? [])].sort(
    (a, b) => bodyOrder.indexOf(a.value) - bodyOrder.indexOf(b.value),
  ),
)
const regions = computed(() =>
  [...(props.facets?.regions ?? [])]
    .sort((a, b) => b.count - a.count)
    .map((r) => r.value),
)

// The list filters toggle a value in or out.
type ListKey = 'fuel' | 'gearbox' | 'body'
function has(key: ListKey, value: string) {
  return query.value[key]?.includes(value) ?? false
}
function toggle(key: ListKey, value: string) {
  const now = query.value[key] ?? []
  query.value = {
    ...query.value,
    [key]: now.includes(value)
      ? now.filter((v) => v !== value)
      : [...now, value],
  }
}
function setBuy(b: GemBuy) {
  query.value = { ...query.value, buy: query.value.buy === b ? '' : b }
}
function setNumber(key: keyof GemQuery, event: Event) {
  const raw = (event.target as HTMLInputElement).value
  const n = raw === '' ? undefined : Math.max(0, Number(raw))
  query.value = { ...query.value, [key]: Number.isFinite(n) ? n : undefined }
}
function setMake(make: string) {
  query.value = { ...query.value, make, model: '' }
}

const groups = computed<
  { key: ListKey; label: string; items: GemCount[]; prefix: string }[]
>(() => [
  {
    key: 'body',
    label: t('gems.body'),
    items: bodies.value,
    prefix: 'gems.bodies',
  },
  {
    key: 'fuel',
    label: t('gems.fuel'),
    items: props.facets?.fuels ?? [],
    prefix: 'gems.fuels',
  },
  {
    key: 'gearbox',
    label: t('gems.gearbox'),
    items: props.facets?.gearboxes ?? [],
    prefix: 'gems.gearboxes',
  },
])
</script>

<template>
  <div class="space-y-5">
    <fieldset>
      <legend class="field-label">{{ t('gems.verdict') }}</legend>
      <div class="flex flex-wrap gap-1.5">
        <button
          v-for="b in buys"
          :key="b"
          type="button"
          class="chip"
          :aria-pressed="query.buy === b"
          @click="setBuy(b)"
        >
          {{ t(`gems.buy.${b}`) }}
        </button>
      </div>
    </fieldset>

    <div class="grid grid-cols-2 gap-2">
      <label class="col-span-2 sm:col-span-1 lg:col-span-2">
        <span class="field-label">{{ t('gems.make') }}</span>
        <select
          class="field"
          :value="query.make ?? ''"
          @change="setMake(($event.target as HTMLSelectElement).value)"
        >
          <option value="">{{ t('gems.anyMake') }}</option>
          <option v-for="m in makes" :key="m.make" :value="m.make">
            {{ m.make }} ({{ formatNumber(m.count) }})
          </option>
        </select>
      </label>
      <label class="col-span-2 sm:col-span-1 lg:col-span-2">
        <span class="field-label">{{ t('gems.model') }}</span>
        <select
          class="field"
          :value="query.model ?? ''"
          :disabled="!query.make"
          @change="
            query = {
              ...query,
              model: ($event.target as HTMLSelectElement).value,
            }
          "
        >
          <option value="">{{ t('gems.anyModel') }}</option>
          <option v-for="m in models" :key="m.model" :value="m.model">
            {{ m.model }} ({{ formatNumber(m.count) }})
          </option>
        </select>
      </label>
    </div>

    <fieldset
      v-for="range in [
        { label: t('gems.price'), min: 'minPrice', max: 'maxPrice', step: 500 },
        { label: t('gems.year'), min: 'minYear', max: 'maxYear', step: 1 },
      ] as const"
      :key="range.min"
    >
      <legend class="field-label">{{ range.label }}</legend>
      <div class="grid grid-cols-2 gap-2">
        <input
          type="number"
          min="0"
          :step="range.step"
          class="field tabular-nums"
          :placeholder="t('gems.from')"
          :aria-label="`${range.label}: ${t('gems.from')}`"
          :value="query[range.min] ?? ''"
          @change="setNumber(range.min, $event)"
        />
        <input
          type="number"
          min="0"
          :step="range.step"
          class="field tabular-nums"
          :placeholder="t('gems.to')"
          :aria-label="`${range.label}: ${t('gems.to')}`"
          :value="query[range.max] ?? ''"
          @change="setNumber(range.max, $event)"
        />
      </div>
    </fieldset>

    <label class="block">
      <span class="field-label">{{ t('gems.mileage') }}</span>
      <input
        type="number"
        min="0"
        step="10000"
        class="field tabular-nums"
        :placeholder="t('gems.maxKm')"
        :value="query.maxMileage ?? ''"
        @change="setNumber('maxMileage', $event)"
      />
    </label>

    <div
      class="flex items-start gap-2 rounded-md bg-brand-50 px-3 py-2.5 text-xs text-gray-700"
    >
      <Info :size="14" class="mt-px shrink-0 text-brand-600" />
      <span>{{ t('gems.detailHint', { n: detailed, all }) }}</span>
    </div>

    <label class="block">
      <span class="field-label">{{ t('gems.region') }}</span>
      <MultiSelect
        :model-value="query.region ?? []"
        :options="regions"
        :placeholder="t('gems.regionPlaceholder')"
        @update:model-value="query = { ...query, region: $event }"
      />
    </label>

    <fieldset v-for="g in groups" v-show="g.items.length" :key="g.key">
      <legend class="field-label">{{ g.label }}</legend>
      <div class="flex flex-wrap gap-1.5">
        <button
          v-for="item in g.items"
          :key="item.value"
          type="button"
          class="chip"
          :aria-pressed="has(g.key, item.value)"
          @click="toggle(g.key, item.value)"
        >
          {{ tKey(`${g.prefix}.${item.value}`, item.value) }}
          <span class="text-gray-500 tabular-nums">{{
            formatNumber(item.count)
          }}</span>
        </button>
      </div>
    </fieldset>

    <label class="flex items-center justify-between gap-3">
      <span class="text-small font-medium text-gray-800">{{
        t('gems.inspectedOnly')
      }}</span>
      <ToggleSwitch
        :model-value="!!query.inspected"
        :label="t('gems.inspectedOnly')"
        @update:model-value="query = { ...query, inspected: $event }"
      />
    </label>

    <label class="flex items-center justify-between gap-3">
      <span class="text-small font-medium text-gray-800">{{
        t('gems.detailedOnly')
      }}</span>
      <ToggleSwitch
        :model-value="!!query.detailed"
        :label="t('gems.detailedOnly')"
        @update:model-value="query = { ...query, detailed: $event }"
      />
    </label>
  </div>
</template>
