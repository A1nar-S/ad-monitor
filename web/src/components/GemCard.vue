<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ArrowUpRight,
  ChevronDown,
  ImageOff,
  Info,
  MapPin,
} from 'lucide-vue-next'
import type { Gem, GemBuy, GemSpec } from '../api'
import { formatNumber, t, tKey } from '../i18n'

// One rated listing: photo, price against the market, the verdict and its
// score, the specs its category shows, and on demand how the score was made.
const props = defineProps<{ gem: Gem; spec: GemSpec; category: string }>()

const open = ref(false)
const broken = ref(false)

const verdict: Record<GemBuy, string> = {
  good: 'bg-success-50 text-success-700 ring-success-100',
  average: 'bg-gray-100 text-gray-700 ring-gray-150',
  bad: 'bg-danger-50 text-danger-700 ring-danger-100',
  check: 'bg-warning-50 text-warning-700 ring-warning-100',
}
const ring: Record<GemBuy, string> = {
  good: 'text-success-600',
  average: 'text-gray-400',
  bad: 'text-danger-600',
  check: 'text-warning-600',
}
const tones = {
  good: 'bg-success-50 text-success-700',
  bad: 'bg-danger-50 text-danger-700',
}

const market = computed(() => {
  const d = props.gem.priceDiff
  if (d === null) return null
  const pct = Math.round(Math.abs(d) * 100)
  if (pct < 2) return { text: t('gems.atMarket'), tone: 'text-gray-500' }
  return d < 0
    ? { text: t('gems.belowMarket', { pct }), tone: 'text-success-700' }
    : { text: t('gems.aboveMarket', { pct }), tone: 'text-danger-700' }
})

// The card fields the gem has, formatted: numbers with their unit, words
// in the reader's language.
const specs = computed(() =>
  props.spec.fields
    .filter((f) => f.card && props.gem.attrs[f.key] !== undefined)
    .map((f) => {
      const v = props.gem.attrs[f.key]!
      let text: string
      if (typeof v === 'number') {
        const n = formatNumber(
          v,
          f.unit === 'l' ? { minimumFractionDigits: 1 } : {},
        )
        text = f.unit ? tKey(`gems.units.${f.unit}`, n, { n }) : String(v)
      } else text = tKey(`gems.values.${f.key}.${v}`, v)
      const tone = f.tones?.[String(v)]
      return {
        key: f.key,
        text,
        pin: f.icon === 'pin',
        tone: tone ? tones[tone] : 'bg-gray-100',
      }
    }),
)

// The breakdown's bars share one scale: the largest weight, 3 points.
const maxPoints = 3
const parts = computed(() =>
  props.gem.components.map((c) => ({
    ...c,
    label: tKey(`gems.components.${c.key}`, c.key),
    width: `${Math.min(1, Math.abs(c.points) / maxPoints) * 50}%`,
  })),
)
const formula = computed(() => tKey(`gems.formulas.${props.category}`, ''))
const verdictLines = computed(() => props.gem.fields.slice(1))
</script>

<template>
  <li class="card flex flex-col overflow-hidden sm:flex-row">
    <a
      :href="gem.url"
      target="_blank"
      rel="noopener noreferrer"
      class="relative grid aspect-[4/3] shrink-0 place-items-center bg-gray-100 text-gray-400 sm:aspect-auto sm:w-48"
      :aria-label="gem.heading"
    >
      <img
        v-if="gem.imageUrl && !broken"
        :src="gem.imageUrl"
        alt=""
        class="absolute inset-0 size-full object-cover"
        loading="lazy"
        referrerpolicy="no-referrer"
        @error="broken = true"
      />
      <ImageOff v-else :size="20" />
    </a>

    <div class="flex min-w-0 flex-1 flex-col p-4">
      <div class="flex items-start gap-3">
        <div class="min-w-0 flex-1">
          <a
            :href="gem.url"
            target="_blank"
            rel="noopener noreferrer"
            class="group inline-flex max-w-full items-center gap-1 text-base font-semibold text-gray-900 hover:text-brand-600"
          >
            <span class="truncate">{{ gem.heading }}</span>
            <ArrowUpRight
              :size="15"
              class="shrink-0 text-gray-400 group-hover:text-brand-600"
            />
          </a>
          <p class="truncate text-small text-gray-500">{{ gem.title }}</p>
        </div>
        <div class="shrink-0 text-right">
          <p class="text-base font-semibold text-gray-900 tabular-nums">
            {{ gem.price }}
          </p>
          <p
            v-if="market"
            class="text-small font-medium tabular-nums"
            :class="market.tone"
          >
            {{ market.text }}
          </p>
        </div>
      </div>

      <ul class="mt-2 flex flex-wrap gap-1.5 text-small text-gray-700">
        <li
          v-for="s in specs"
          :key="s.key"
          class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 tabular-nums"
          :class="s.tone"
        >
          <MapPin v-if="s.pin" :size="12" class="text-gray-500" />{{ s.text }}
        </li>
        <li
          v-if="!gem.detailed"
          class="inline-flex items-center gap-1 px-1 py-0.5 text-gray-500"
          :title="t('gems.listOnly')"
        >
          <Info :size="12" />{{ t('gems.listOnly') }}
        </li>
      </ul>

      <div class="mt-3 flex items-center gap-3">
        <span
          class="relative grid size-10 shrink-0 place-items-center"
          :title="t('gems.score', { score: gem.score })"
          :aria-label="t('gems.score', { score: gem.score })"
        >
          <svg viewBox="0 0 36 36" class="absolute inset-0 -rotate-90">
            <circle
              cx="18"
              cy="18"
              r="15.5"
              fill="none"
              stroke-width="3"
              class="stroke-gray-150"
            />
            <circle
              cx="18"
              cy="18"
              r="15.5"
              fill="none"
              stroke-width="3"
              stroke-linecap="round"
              stroke="currentColor"
              :class="ring[gem.buy]"
              :stroke-dasharray="`${(gem.score / 100) * 97.4} 97.4`"
            />
          </svg>
          <span class="text-small font-semibold text-gray-900 tabular-nums">{{
            gem.score
          }}</span>
        </span>
        <span
          class="badge shrink-0 ring-1 ring-inset"
          :class="verdict[gem.buy]"
          >{{ t(`gems.buy.${gem.buy}`) }}</span
        >
        <p class="min-w-0 flex-1 text-small text-gray-700">
          {{ gem.overall.replace(/^[^:]*:\s*/, '') }}
        </p>
      </div>

      <button
        class="mt-2 inline-flex items-center gap-1 self-start text-small font-medium text-brand-600 hover:underline"
        :aria-expanded="open"
        @click="open = !open"
      >
        {{ open ? t('gems.hide') : t('gems.breakdown') }}
        <ChevronDown
          :size="14"
          class="transition-transform"
          :class="{ 'rotate-180': open }"
        />
      </button>

      <div v-if="open" class="mt-3 space-y-4 border-t border-gray-150 pt-3">
        <ul class="space-y-2" :aria-label="t('gems.breakdown')">
          <li
            v-for="p in parts"
            :key="p.key"
            class="grid grid-cols-[8.5rem_1fr_3rem] items-center gap-3 text-small"
          >
            <span class="truncate text-gray-700">{{ p.label }}</span>
            <span
              class="relative h-2 rounded-full bg-gray-100"
              aria-hidden="true"
            >
              <span class="absolute inset-y-0 left-1/2 w-px bg-gray-300" />
              <span
                class="absolute inset-y-0 rounded-full"
                :class="
                  p.points >= 0
                    ? 'left-1/2 bg-success-600'
                    : 'right-1/2 bg-danger-600'
                "
                :style="{ width: p.width }"
              />
            </span>
            <span
              class="text-right font-medium tabular-nums"
              :class="
                p.points > 0.05
                  ? 'text-success-700'
                  : p.points < -0.05
                    ? 'text-danger-700'
                    : 'text-gray-500'
              "
              >{{ p.points > 0 ? '+' : ''
              }}{{ formatNumber(p.points, { maximumFractionDigits: 1 }) }}</span
            >
          </li>
        </ul>
        <p v-if="formula" class="text-xs text-gray-500">{{ formula }}</p>
        <dl class="space-y-2 text-small">
          <div v-for="f in verdictLines" :key="f.name">
            <dt class="font-medium text-gray-900">{{ f.name }}</dt>
            <dd class="text-gray-700">{{ f.value }}</dd>
          </div>
        </dl>
      </div>
    </div>
  </li>
</template>
