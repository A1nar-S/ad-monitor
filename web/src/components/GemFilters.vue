<script setup lang="ts">
import { computed } from 'vue'
import { Info } from 'lucide-vue-next'
import MultiSelect from './MultiSelect.vue'
import ToggleSwitch from './ToggleSwitch.vue'
import type { GemBuy, GemField, GemPage, GemQuery, GemSpec } from '../api'
import { formatNumber, t, tKey } from '../i18n'

// The gem finder's filters, drawn from the category's spec: the parent owns
// the query (v-model) and loads the gems when it changes; facets say which
// values the gems the other filters let through have, with counts, so every
// filter narrows what the others offer.
const props = defineProps<{
  spec: GemSpec
  facets: GemPage['facets'] | null
  buys: GemPage['buys'] | null
  detailed: number
  all: number
}>()
const query = defineModel<GemQuery>({ required: true })

const buys: GemBuy[] = ['good', 'average', 'bad', 'check']

const label = (key: string) => tKey(`gems.fields.${key}`, key)
const valueLabel = (key: string, v: string) =>
  tKey(`gems.values.${key}.${v}`, v)

// The detail fields are known only for read ad pages: the hint goes above
// the first of them.
const details = computed(() => props.spec.fields.filter((f) => f.detail))
const firstDetail = computed(() => details.value[0]?.key)

const picked = (key: string) => query.value.choices[key] ?? []

// The values a choice offers: those the gems have (with counts), narrowed
// by the choice it depends on. A picked value stays, at 0 if the other
// filters left none, so it can still be unpicked.
function options(f: GemField) {
  let all = props.facets?.[f.key] ?? []
  if (f.dependsOn) {
    const parent = picked(f.dependsOn)[0]
    all = parent ? all.filter((x) => x.parent === parent) : []
  }
  const counts = new Map<string, number>()
  for (const x of all) counts.set(x.value, (counts.get(x.value) ?? 0) + x.count)
  for (const v of picked(f.key)) if (!counts.has(v)) counts.set(v, 0)
  return [...counts].map(([value, count]) => ({ value, count }))
}
// A searched choice offers the whole catalog (every place, not just those
// with a read ad page), those the other filters leave gems at first, most
// first.
function searchOptions(f: GemField) {
  const chosen = picked(f.key)
  const have = options(f)
    .filter((o) => o.count > 0 || chosen.includes(o.value))
    .sort((a, b) => b.count - a.count)
    .map((o) => o.value)
  if (!f.options) return have
  const seen = new Set(have)
  return [...have, ...f.options.filter((o) => !seen.has(o))]
}
const buyCount = (b: GemBuy) =>
  props.buys ? (props.buys.find((x) => x.value === b)?.count ?? 0) : null

// Setting a choice clears those that depend on it.
function setChoice(key: string, values: string[]) {
  const choices = { ...query.value.choices, [key]: values }
  if (!values.length) delete choices[key]
  for (const f of props.spec.fields)
    if (f.dependsOn === key) delete choices[f.key]
  query.value = { ...query.value, choices }
}
function toggle(key: string, value: string) {
  const now = picked(key)
  setChoice(
    key,
    now.includes(value) ? now.filter((v) => v !== value) : [...now, value],
  )
}
function selected(key: string, event: Event) {
  const v = (event.target as HTMLSelectElement).value
  setChoice(key, v ? [v] : [])
}
function number(event: Event) {
  const n = Number((event.target as HTMLInputElement).value)
  return Number.isFinite(n) && n > 0 ? n : undefined
}
function setRange(key: string, end: 'min' | 'max', event: Event) {
  const ranges = {
    ...query.value.ranges,
    [key]: { ...query.value.ranges[key], [end]: number(event) },
  }
  query.value = { ...query.value, ranges }
}
function setPrice(end: 'minPrice' | 'maxPrice', event: Event) {
  query.value = { ...query.value, [end]: number(event) }
}
function setBuy(b: GemBuy) {
  query.value = { ...query.value, buy: query.value.buy === b ? '' : b }
}
const ends = [
  { id: 'min', label: () => t('gems.from') },
  { id: 'max', label: () => t('gems.to') },
] as const
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
          <span
            v-if="buyCount(b) !== null"
            class="text-gray-500 tabular-nums"
            >{{ formatNumber(buyCount(b)!) }}</span
          >
        </button>
      </div>
    </fieldset>

    <fieldset>
      <legend class="field-label">{{ t('gems.price') }}</legend>
      <div class="grid grid-cols-2 gap-2">
        <input
          v-for="end in ends"
          :key="end.id"
          type="number"
          min="0"
          step="500"
          class="field tabular-nums"
          :placeholder="end.label()"
          :aria-label="`${t('gems.price')}: ${end.label()}`"
          :value="(end.id === 'min' ? query.minPrice : query.maxPrice) ?? ''"
          @change="setPrice(end.id === 'min' ? 'minPrice' : 'maxPrice', $event)"
        />
      </div>
    </fieldset>

    <template v-for="f in spec.fields" :key="f.key">
      <div
        v-if="f.key === firstDetail"
        class="flex items-start gap-2 rounded-md bg-brand-50 px-3 py-2.5 text-xs text-gray-700"
      >
        <Info :size="14" class="mt-px shrink-0 text-brand-600" />
        <span>{{
          t('gems.detailHint', {
            fields: details.map((d) => label(d.key)).join(', '),
            n: detailed,
            all,
          })
        }}</span>
      </div>

      <label v-if="f.kind === 'choice' && f.control === 'select'" class="block">
        <span class="field-label">{{ label(f.key) }}</span>
        <select
          class="field"
          :value="picked(f.key)[0] ?? ''"
          :disabled="!!f.dependsOn && !picked(f.dependsOn).length"
          @change="selected(f.key, $event)"
        >
          <option value="">{{ t('gems.any') }}</option>
          <option v-for="o in options(f)" :key="o.value" :value="o.value">
            {{ valueLabel(f.key, o.value) }} ({{ formatNumber(o.count) }})
          </option>
        </select>
      </label>

      <label
        v-else-if="f.kind === 'choice' && f.control === 'search'"
        class="block"
      >
        <span class="field-label">{{ label(f.key) }}</span>
        <MultiSelect
          :model-value="picked(f.key)"
          :options="searchOptions(f)"
          :placeholder="t('gems.search')"
          @update:model-value="setChoice(f.key, $event)"
        />
      </label>

      <fieldset v-else-if="f.kind === 'choice'" v-show="options(f).length">
        <legend class="field-label">{{ label(f.key) }}</legend>
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="o in options(f)"
            :key="o.value"
            type="button"
            class="chip"
            :aria-pressed="picked(f.key).includes(o.value)"
            @click="toggle(f.key, o.value)"
          >
            {{ valueLabel(f.key, o.value) }}
            <span class="text-gray-500 tabular-nums">{{
              formatNumber(o.count)
            }}</span>
          </button>
        </div>
      </fieldset>

      <label v-else-if="f.control === 'max'" class="block">
        <span class="field-label">{{ label(f.key) }}</span>
        <input
          type="number"
          min="0"
          class="field tabular-nums"
          :placeholder="t('gems.upTo')"
          :value="query.ranges[f.key]?.max ?? ''"
          @change="setRange(f.key, 'max', $event)"
        />
      </label>

      <fieldset v-else>
        <legend class="field-label">{{ label(f.key) }}</legend>
        <div class="grid grid-cols-2 gap-2">
          <input
            v-for="end in ends"
            :key="end.id"
            type="number"
            min="0"
            step="any"
            class="field tabular-nums"
            :placeholder="end.label()"
            :aria-label="`${label(f.key)}: ${end.label()}`"
            :value="query.ranges[f.key]?.[end.id] ?? ''"
            @change="setRange(f.key, end.id, $event)"
          />
        </div>
      </fieldset>
    </template>

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
