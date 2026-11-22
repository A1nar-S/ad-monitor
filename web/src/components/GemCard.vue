<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ArrowUpRight,
  Check,
  ChevronDown,
  CircleAlert,
  ClipboardCheck,
  Cog,
  Gauge,
  ImageOff,
  Info,
  Joystick,
  ListChecks,
  MapPin,
  Tag,
  X,
} from 'lucide-vue-next'
import type { Gem, GemBuy, GemLine, GemSpec, GemTone } from '../api'
import { formatNumber, t, tKey } from '../i18n'

// One rated listing: photo, price against the market, the verdict and its
// score, the specs its category shows, and on demand how the score was made.
const props = defineProps<{ gem: Gem; spec: GemSpec }>()

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
  // The gap in euros too: 20% is 300 € on one car and 5 000 € on another.
  const p = props.gem.priceValue
  const amount = `${formatNumber(Math.round(Math.abs(p - p / (1 + d))))} €`
  return d < 0
    ? { text: t('gems.belowMarket', { pct, amount }), tone: 'text-success-700' }
    : { text: t('gems.aboveMarket', { pct, amount }), tone: 'text-danger-700' }
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

// The breakdown in score points: from the neutral 50, what each part adds
// or takes, rounded so the parts add up to the score shown (unless the score
// hit 0 or 100). Bars share one scale: the largest part there can be,
// price's 15 points.
const neutral = 50
const maxPart = 15
const parts = computed(() => {
  const raw = props.gem.components.map((c) => c.score ?? c.points * 5)
  const shown = raw.map((x) => Math.round(x))
  const target = props.gem.score - neutral
  const sum = raw.reduce((a, b) => a + b, 0)
  if (Math.round(neutral + sum) === props.gem.score) {
    // Hand the rounding slack to the parts rounded furthest from their value.
    let slack = target - shown.reduce((a, b) => a + b, 0)
    const order = raw
      .map((x, i) => ({ i, off: x - shown[i]! }))
      .sort((a, b) => Math.abs(b.off) - Math.abs(a.off))
    for (const { i, off } of order) {
      if (slack === 0) break
      if (Math.sign(off) === Math.sign(slack)) {
        shown[i]! += Math.sign(slack)
        slack -= Math.sign(slack)
      }
    }
  }
  return props.gem.components.map((c, i) => ({
    ...c,
    value: shown[i]!,
    label: tKey(`gems.components.${c.key}`, c.key),
    width: `${Math.min(1, Math.abs(raw[i]!) / maxPart) * 50}%`,
    line: props.gem.lines.find((l) => l.key === c.key) as GemLine | undefined,
    icon: icons[c.key] ?? Info,
  }))
})
const signed = (n: number) => (n > 0 ? `+${n}` : n < 0 ? `−${-n}` : '0')

// Each part of the score with its verdict: an icon per part, the verdict's
// label coloured by its tone.
const icons: Record<string, typeof Tag> = {
  price: Tag,
  mileage: Gauge,
  inspection: ClipboardCheck,
  engine: Cog,
  gearbox: Joystick,
  equipment: ListChecks,
}
const toneClass: Record<GemTone, string> = {
  great: 'bg-success-50 text-success-700 ring-success-100',
  good: 'bg-success-50 text-success-700 ring-success-100',
  neutral: 'bg-gray-100 text-gray-700 ring-gray-150',
  mixed: 'bg-warning-50 text-warning-700 ring-warning-100',
  warn: 'bg-warning-50 text-warning-700 ring-warning-100',
  bad: 'bg-danger-50 text-danger-700 ring-danger-100',
  awful: 'bg-danger-50 text-danger-700 ring-danger-100',
}
const iconTone: Record<GemTone, string> = {
  great: 'bg-success-50 text-success-600',
  good: 'bg-success-50 text-success-600',
  neutral: 'bg-gray-100 text-gray-500',
  mixed: 'bg-warning-50 text-warning-600',
  warn: 'bg-warning-50 text-warning-600',
  bad: 'bg-danger-50 text-danger-600',
  awful: 'bg-danger-50 text-danger-600',
}
</script>

<template>
  <li class="card flex flex-col p-4">
    <div class="flex min-w-0 flex-1 flex-col">
      <div class="flex items-start gap-3">
        <!-- ss.com gives only a small list-page photo: shown at about its
             own size, so it stays sharp. -->
        <a
          :href="gem.url"
          target="_blank"
          rel="noopener noreferrer"
          class="grid h-15 w-20 shrink-0 place-items-center overflow-hidden rounded-md bg-gray-100 text-gray-400"
          :aria-label="gem.heading"
          tabindex="-1"
        >
          <img
            v-if="gem.imageUrl && !broken"
            :src="gem.imageUrl"
            alt=""
            class="size-full object-cover"
            loading="lazy"
            referrerpolicy="no-referrer"
            @error="broken = true"
          />
          <ImageOff v-else :size="16" />
        </a>
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

      <div v-if="open" class="mt-3 border-t border-gray-150 pt-3">
        <div
          class="flex items-center justify-between pb-2 text-small text-gray-500"
        >
          <span>{{ t('gems.neutral') }} · {{ t('gems.neutralHint') }}</span>
          <span class="font-medium text-gray-900 tabular-nums">{{
            neutral
          }}</span>
        </div>
        <ul
          class="divide-y divide-gray-150 border-y border-gray-150"
          :aria-label="t('gems.breakdown')"
        >
          <li v-for="p in parts" :key="p.key" class="flex gap-3 py-3">
            <span
              class="grid size-8 shrink-0 place-items-center rounded-full"
              :class="iconTone[p.line?.tone ?? 'neutral']"
              aria-hidden="true"
            >
              <component :is="p.icon" :size="16" />
            </span>
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span class="text-sm font-medium text-gray-900">{{
                  p.label
                }}</span>
                <span
                  v-if="p.line"
                  class="badge ring-1 ring-inset"
                  :class="toneClass[p.line.tone]"
                  >{{ p.line.label }}</span
                >
                <span
                  class="ml-auto text-sm font-semibold tabular-nums"
                  :class="
                    p.value > 0
                      ? 'text-success-700'
                      : p.value < 0
                        ? 'text-danger-700'
                        : 'text-gray-500'
                  "
                  >{{ signed(p.value) }}</span
                >
              </div>
              <span
                class="relative mt-2 block h-1.5 rounded-full bg-gray-100"
                aria-hidden="true"
              >
                <span class="absolute inset-y-0 left-1/2 w-px bg-gray-300" />
                <span
                  class="absolute inset-y-0 rounded-full"
                  :class="
                    p.value >= 0
                      ? 'left-1/2 bg-success-600'
                      : 'right-1/2 bg-danger-600'
                  "
                  :style="{ width: p.width }"
                />
              </span>
              <p
                v-if="p.line?.subject || p.line?.text"
                class="mt-2 text-small text-gray-700"
              >
                <span v-if="p.line.subject" class="font-medium text-gray-900"
                  >{{ p.line.subject }}.
                </span>
                {{ p.line.text }}
              </p>
              <p v-else-if="p.why" class="mt-2 text-small text-gray-700">
                {{ p.why }}
              </p>
              <div
                v-if="p.line?.has?.length || p.line?.lacks?.length"
                class="mt-2 space-y-1.5"
              >
                <ul
                  v-if="p.line.has?.length"
                  class="flex flex-wrap gap-1.5"
                  :aria-label="t('gems.has')"
                >
                  <li
                    v-for="f in p.line.has"
                    :key="f"
                    class="inline-flex items-center gap-1 rounded bg-success-50 px-1.5 py-0.5 text-xs text-success-700"
                  >
                    <Check :size="12" />{{ f }}
                  </li>
                </ul>
                <ul
                  v-if="p.line.lacks?.length"
                  class="flex flex-wrap gap-1.5"
                  :aria-label="t('gems.lacks')"
                >
                  <li
                    v-for="f in p.line.lacks"
                    :key="f"
                    class="inline-flex items-center gap-1 rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600"
                  >
                    <X :size="12" />{{ f }}
                  </li>
                </ul>
              </div>
              <p
                v-if="p.line?.unticked?.length"
                class="mt-2 flex items-start gap-1.5 text-xs text-warning-700"
              >
                <CircleAlert :size="13" class="mt-px shrink-0" />
                {{ t('gems.unticked', { list: p.line.unticked.join(', ') }) }}
              </p>
            </div>
          </li>
        </ul>
        <div class="flex items-center justify-between pt-2 text-sm">
          <span class="font-medium text-gray-900">{{ t('gems.total') }}</span>
          <span class="font-semibold text-gray-900 tabular-nums">{{
            gem.score
          }}</span>
        </div>
      </div>
    </div>
  </li>
</template>
