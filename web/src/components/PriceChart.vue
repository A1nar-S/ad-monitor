<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { PricePoint } from '../api'
import { formatDate, formatMoney, t } from '../i18n'

// A watch's price over time. The history holds a point per change, so the
// line is a step: the price stays what it was until the next point. Reads
// that failed are gaps with a grey band, never a drop to zero.
const props = defineProps<{
  history: PricePoint[] // newest first
  currency: string
  thresholdCents: number
}>()

const height = 220
const pad = { top: 16, right: 16, bottom: 26, left: 64 }
const box = ref<HTMLDivElement>()
const width = ref(640)
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = new ResizeObserver(([entry]) => {
    if (entry) width.value = Math.max(280, entry.contentRect.width)
  })
  if (box.value) observer.observe(box.value)
})
onBeforeUnmount(() => observer?.disconnect())

type Span = {
  from: number
  to: number
  cents: number | null
  raw: string
  error: string
}

// Oldest first, each point lasting until the next one (the last until now).
const spans = computed<Span[]>(() => {
  const points = [...props.history].reverse()
  const now = Date.now()
  return points.map((p, i) => ({
    from: new Date(p.at).getTime(),
    to: i + 1 < points.length ? new Date(points[i + 1]!.at).getTime() : now,
    cents: p.ok ? p.cents : null,
    raw: p.raw,
    error: p.error,
  }))
})

const domain = computed(() => {
  const s = spans.value
  const first = s[0]?.from ?? Date.now()
  const x1 = s[s.length - 1]?.to ?? Date.now()
  // Short histories get an hour of room before them, never time to come.
  const x0 = Math.min(first, x1 - 3600e3)
  const values = s.flatMap((p) => (p.cents === null ? [] : [p.cents]))
  values.push(props.thresholdCents)
  let lo = Math.min(...values)
  let hi = Math.max(...values)
  if (hi === lo) {
    lo -= Math.max(100, lo * 0.05)
    hi += Math.max(100, hi * 0.05)
  }
  const ticks = niceTicks(lo, hi)
  return { x0, x1, y0: ticks[0]!, y1: ticks[ticks.length - 1]!, ticks }
})

// Round axis steps (1, 2 or 5 × a power of ten), in whole units.
function niceTicks(lo: number, hi: number): number[] {
  const raw = (hi - lo) / 4
  const mag = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw
  const start = Math.floor(lo / step) * step
  const out: number[] = []
  for (let v = start; v <= hi + step * 0.5; v += step) out.push(Math.round(v))
  if (out[out.length - 1]! < hi) out.push(out[out.length - 1]! + step)
  return out
}

const plotW = computed(() => width.value - pad.left - pad.right)
const plotH = height - pad.top - pad.bottom
const x = (ms: number) =>
  pad.left +
  ((ms - domain.value.x0) / (domain.value.x1 - domain.value.x0)) * plotW.value
const y = (cents: number) =>
  pad.top +
  (1 - (cents - domain.value.y0) / (domain.value.y1 - domain.value.y0)) * plotH

// The step line, broken where reads failed.
const runs = computed(() => {
  const out: { line: string; area: string }[] = []
  let line = ''
  let area = ''
  let startX = 0
  let lastX = 0
  const flush = () => {
    if (line) {
      const base = pad.top + plotH
      out.push({ line, area: `${area} L${lastX},${base} L${startX},${base} Z` })
    }
    line = area = ''
  }
  for (const s of spans.value) {
    if (s.cents === null) {
      flush()
      continue
    }
    const x0 = x(s.from)
    const x1 = x(s.to)
    const yy = y(s.cents)
    if (!line) {
      line = `M${x0},${yy}`
      area = `M${x0},${yy}`
      startX = x0
    } else {
      line += ` V${yy}`
      area += ` V${yy}`
    }
    line += ` H${x1}`
    area += ` H${x1}`
    lastX = x1
  }
  flush()
  return out
})

const gaps = computed(() =>
  spans.value
    .filter((s) => s.cents === null)
    .map((s) => ({ x: x(s.from), w: Math.max(2, x(s.to) - x(s.from)) })),
)

const last = computed(() => {
  const s = spans.value[spans.value.length - 1]
  return s && s.cents !== null
    ? { x: x(s.to), y: y(s.cents), cents: s.cents }
    : null
})

// Time ticks fall on whole hours or local midnights, at the smallest step
// that fits the width.
const hour = 3600e3
const day = 24 * hour
const xStep = computed(() => {
  const { x0, x1 } = domain.value
  const n = Math.max(2, Math.min(6, Math.floor(plotW.value / 110)))
  const steps = [1, 2, 3, 6, 12].map((h) => h * hour)
  steps.push(...[1, 2, 7, 14, 30, 91].map((d) => d * day))
  return steps.find((s) => (x1 - x0) / s <= n) ?? 365 * day
})
const short = computed(() => xStep.value < day)
const xTicks = computed(() => {
  const { x0, x1 } = domain.value
  const step = xStep.value
  const start = new Date(x0)
  start.setMinutes(0, 0, 0)
  if (step >= day) start.setHours(0)
  const out: number[] = []
  for (let t = start.getTime(); t <= x1 && out.length < 60;) {
    const d = new Date(t)
    if (t >= x0 && (step >= day || d.getHours() % (step / hour) === 0))
      out.push(t)
    // Calendar steps, so daylight-saving days stay on midnight.
    if (step >= day) d.setDate(d.getDate() + (out.length ? step / day : 1))
    else d.setHours(d.getHours() + 1)
    t = d.getTime()
  }
  return out
})
const anchor = (ms: number) =>
  x(ms) - pad.left < 24
    ? 'start'
    : pad.left + plotW.value - x(ms) < 24
      ? 'end'
      : 'middle'
const dateLabel = (ms: number) =>
  formatDate(
    ms,
    short.value
      ? { hour: '2-digit', minute: '2-digit' }
      : { day: 'numeric', month: 'short' },
  )
const money = (cents: number) => formatMoney(cents, props.currency)

// --- hover: a crosshair that finds the time, and its price ---------------------

const hover = ref<{ x: number; span: Span; at: number } | null>(null)
function onMove(event: PointerEvent) {
  const svg = event.currentTarget as SVGSVGElement
  const rect = svg.getBoundingClientRect()
  const px = ((event.clientX - rect.left) / rect.width) * width.value
  if (px < pad.left || px > pad.left + plotW.value) {
    hover.value = null
    return
  }
  const { x0, x1 } = domain.value
  const at = x0 + ((px - pad.left) / plotW.value) * (x1 - x0)
  const span = spans.value.find((s) => at >= s.from && at <= s.to)
  hover.value = span ? { x: px, span, at } : null
}
const tooltipLeft = computed(() =>
  hover.value ? Math.min(hover.value.x + 12, width.value - 200) : 0,
)
</script>

<template>
  <div ref="box" class="relative">
    <svg
      :width="width"
      :height="height"
      :viewBox="`0 0 ${width} ${height}`"
      class="block touch-none select-none"
      role="img"
      :aria-label="t('chart.label')"
      @pointermove="onMove"
      @pointerleave="hover = null"
    >
      <!-- grid and y axis -->
      <g>
        <template v-for="tick in domain.ticks" :key="tick">
          <line
            :x1="pad.left"
            :x2="pad.left + plotW"
            :y1="y(tick)"
            :y2="y(tick)"
            stroke="var(--color-gray-150)"
            stroke-width="1"
          />
          <text
            :x="pad.left - 8"
            :y="y(tick)"
            text-anchor="end"
            dominant-baseline="middle"
            class="fill-gray-500 text-[11px] tabular-nums"
          >
            {{ money(tick) }}
          </text>
        </template>
        <text
          v-for="tick in xTicks"
          :key="tick"
          :x="x(tick)"
          :y="height - 6"
          :text-anchor="anchor(tick)"
          class="fill-gray-500 text-[11px]"
        >
          {{ dateLabel(tick) }}
        </text>
      </g>

      <!-- reads that failed -->
      <rect
        v-for="(g, i) in gaps"
        :key="'gap' + i"
        :x="g.x"
        :y="pad.top"
        :width="g.w"
        :height="plotH"
        fill="var(--color-gray-200)"
        opacity="0.5"
      />

      <!-- the alert line -->
      <line
        :x1="pad.left"
        :x2="pad.left + plotW"
        :y1="y(thresholdCents)"
        :y2="y(thresholdCents)"
        stroke="var(--color-gray-500)"
        stroke-width="1"
      />
      <text
        :x="pad.left + 6"
        :y="y(thresholdCents) - 6"
        class="fill-gray-600 text-[11px]"
      >
        {{ t('chart.alertAt', { price: money(thresholdCents) }) }}
      </text>

      <!-- the price -->
      <g v-for="(r, i) in runs" :key="'run' + i">
        <path :d="r.area" fill="var(--color-brand-600)" opacity="0.1" />
        <path
          :d="r.line"
          fill="none"
          stroke="var(--color-brand-600)"
          stroke-width="2"
          stroke-linejoin="round"
          stroke-linecap="round"
        />
      </g>
      <g v-if="last && !hover">
        <circle
          :cx="last.x"
          :cy="last.y"
          r="4"
          fill="var(--color-brand-600)"
          stroke="var(--color-surface)"
          stroke-width="2"
        />
        <text
          :x="last.x - 8"
          :y="last.y - 10"
          text-anchor="end"
          class="fill-gray-900 text-[12px] font-semibold tabular-nums"
        >
          {{ money(last.cents) }}
        </text>
      </g>

      <!-- hover -->
      <g v-if="hover">
        <line
          :x1="hover.x"
          :x2="hover.x"
          :y1="pad.top"
          :y2="pad.top + plotH"
          stroke="var(--color-gray-400)"
          stroke-width="1"
        />
        <circle
          v-if="hover.span.cents !== null"
          :cx="hover.x"
          :cy="y(hover.span.cents)"
          r="4"
          fill="var(--color-brand-600)"
          stroke="var(--color-surface)"
          stroke-width="2"
        />
      </g>
    </svg>
    <div
      v-if="hover"
      class="pointer-events-none absolute top-2 w-[188px] rounded-md bg-raised px-3 py-2 text-small shadow-raised"
      :style="{ left: tooltipLeft + 'px' }"
    >
      <p class="text-xs text-gray-500">
        {{
          formatDate(hover.at, {
            day: 'numeric',
            month: 'short',
            hour: '2-digit',
            minute: '2-digit',
          })
        }}
      </p>
      <p
        v-if="hover.span.cents !== null"
        class="font-semibold text-gray-900 tabular-nums"
      >
        {{ money(hover.span.cents) }}
      </p>
      <p v-else class="text-gray-700">{{ t('chart.unreadable') }}</p>
    </div>
  </div>
</template>
