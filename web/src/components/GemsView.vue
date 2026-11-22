<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  CircleAlert,
  Gem as GemIcon,
  RefreshCw,
  SlidersHorizontal,
  TriangleAlert,
  X,
} from 'lucide-vue-next'
import BaseDialog from './BaseDialog.vue'
import EmptyState from './EmptyState.vue'
import GemCard from './GemCard.vue'
import GemFilters from './GemFilters.vue'
import PageHeader from './PageHeader.vue'
import TabBar from './TabBar.vue'
import {
  api,
  ApiError,
  gemParams,
  type GemPage,
  type GemQuery,
  type GemScan,
  type GemSort,
  type GemSource,
  type Me,
} from '../api'
import { formatNumber, formatRelative, t, tKey } from '../i18n'
import { toastError } from '../toast'

// The gem finder: every car for sale on a site, rated by the Go side. This
// page sends the filters (kept in the address, so a view can be shared or
// reloaded) and renders what comes back. Admins also start the fetch.
const props = defineProps<{ me: Me }>()

const sources = ref<GemSource[]>([])
const source = ref('')
const category = ref('cars')
const scan = ref<GemScan | null>(null)
const page = ref<GemPage | null>(null)
const error = ref('')
const loading = ref(true)
const drawer = ref(false)

// What the address holds: the filters, the sort and the page.
const listKeys = ['fuel', 'gearbox', 'body', 'region'] as const
const numberKeys = [
  'minPrice',
  'maxPrice',
  'minYear',
  'maxYear',
  'maxMileage',
  'page',
] as const
function fromAddress(): GemQuery {
  const p = new URLSearchParams(location.search)
  const q: GemQuery = {
    make: p.get('make') ?? '',
    model: p.get('model') ?? '',
    buy: (p.get('buy') ?? '') as GemQuery['buy'],
    sort: (p.get('sort') ?? '') as GemSort,
    detailed: p.get('detailed') === '1',
    inspected: p.get('inspected') === '1',
  }
  for (const k of listKeys) q[k] = p.getAll(k)
  for (const k of numberKeys) {
    const n = Number(p.get(k))
    if (n > 0) q[k] = n
  }
  return q
}
const query = ref<GemQuery>(fromAddress())

const running = computed(
  () => scan.value?.status === 'requested' || scan.value?.status === 'running',
)
const siteName = computed(
  () => sources.value.find((s) => s.id === source.value)?.name ?? 'ss.com',
)
const categories = computed(
  () => sources.value.find((s) => s.id === source.value)?.categories ?? [],
)
const pages = computed(() =>
  page.value
    ? Math.max(1, Math.ceil(page.value.total / page.value.pageSize))
    : 1,
)

const status = computed(() => {
  const s = scan.value
  const site = siteName.value
  if (!s)
    return props.me.admin
      ? t('gems.neverScannedAdmin')
      : t('gems.neverScanned', { site })
  if (s.status === 'requested') return t('gems.requested')
  if (s.status === 'running')
    return s.phase === 'detailing'
      ? t('gems.detailing', { done: s.detailed, total: s.detailTotal })
      : t('gems.listing', { pages: s.pages, ads: s.ads })
  if (s.status === 'failed')
    return s.error
      ? t('gems.failed', { error: s.error })
      : t('gems.failedShort')
  const when = formatRelative(s.finishedAt ?? s.requestedAt)
  return props.me.admin
    ? t('gems.doneAdmin', { when, ads: s.ads, rated: s.rated, gone: s.gone })
    : t('gems.done', { when, rated: s.rated })
})
const progress = computed(() => {
  const s = scan.value
  if (!s || s.status !== 'running' || s.phase !== 'detailing') return null
  return s.detailTotal ? s.detailed / s.detailTotal : 0
})

// The filters set, as removable chips above the results.
type Chip = { id: string; label: string; clear: () => void }
const chips = computed<Chip[]>(() => {
  const q = query.value
  const out: Chip[] = []
  const set = (patch: Partial<GemQuery>) =>
    (query.value = { ...query.value, ...patch })
  if (q.buy)
    out.push({
      id: 'buy',
      label: t(`gems.buy.${q.buy}`),
      clear: () => set({ buy: '' }),
    })
  if (q.make)
    out.push({
      id: 'make',
      label: [q.make, q.model].filter(Boolean).join(' '),
      clear: () => set({ make: '', model: '' }),
    })
  const prefix = {
    fuel: 'gems.fuels',
    gearbox: 'gems.gearboxes',
    body: 'gems.bodies',
    region: '',
  }
  for (const k of listKeys)
    for (const v of q[k] ?? [])
      out.push({
        id: `${k}:${v}`,
        label: prefix[k] ? tKey(`${prefix[k]}.${v}`, v) : v,
        clear: () => set({ [k]: (q[k] ?? []).filter((x) => x !== v) }),
      })
  const range = (
    label: string,
    lo: number | undefined,
    hi: number | undefined,
  ) =>
    lo && hi
      ? `${label}: ${formatNumber(lo)}–${formatNumber(hi)}`
      : lo
        ? `${label} ≥ ${formatNumber(lo)}`
        : `${label} ≤ ${formatNumber(hi ?? 0)}`
  if (q.minPrice || q.maxPrice)
    out.push({
      id: 'price',
      label: range(t('gems.price'), q.minPrice, q.maxPrice),
      clear: () => set({ minPrice: undefined, maxPrice: undefined }),
    })
  if (q.minYear || q.maxYear)
    out.push({
      id: 'year',
      label: range(t('gems.year'), q.minYear, q.maxYear),
      clear: () => set({ minYear: undefined, maxYear: undefined }),
    })
  if (q.maxMileage)
    out.push({
      id: 'km',
      label: range(t('gems.mileage'), undefined, q.maxMileage),
      clear: () => set({ maxMileage: undefined }),
    })
  if (q.inspected)
    out.push({
      id: 'inspected',
      label: t('gems.inspectedOnly'),
      clear: () => set({ inspected: false }),
    })
  if (q.detailed)
    out.push({
      id: 'detailed',
      label: t('gems.detailedOnly'),
      clear: () => set({ detailed: false }),
    })
  return out
})

function clearAll() {
  query.value = { sort: query.value.sort }
}

let loadSeq = 0
async function load() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const result = await api.gems({
      ...query.value,
      source: source.value,
      category: category.value,
    })
    if (seq !== loadSeq) return // a newer filter change won
    page.value = result
    error.value = ''
  } catch (reason) {
    if (seq !== loadSeq) return
    error.value =
      reason instanceof ApiError ? reason.message : t('gems.loadFailed')
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
let lastDetailed = -1
async function refreshScan() {
  const wasRunning = running.value
  try {
    scan.value = await api.gemScan(source.value)
  } catch {
    // A missed poll is retried on the next tick.
  }
  const s = scan.value
  // Pass 1's results land at once and pass 2 saves every 100 ad pages:
  // reload as they come, and when the fetch ends.
  if (
    s?.phase === 'detailing' &&
    Math.floor(s.detailed / 100) !== lastDetailed
  ) {
    if (lastDetailed !== -1) load()
    lastDetailed = Math.floor(s.detailed / 100)
  }
  if (wasRunning && !running.value) load()
  clearTimeout(timer)
  // Fast while a fetch runs; slowly otherwise, to notice one an admin starts.
  timer = setTimeout(refreshScan, running.value ? 5000 : 60000)
}

async function startScan() {
  try {
    scan.value = await api.requestGemScan(source.value)
  } catch (reason) {
    toastError(
      reason instanceof ApiError
        ? reason.status === 409
          ? t('gems.busy')
          : reason.message
        : t('gems.loadFailed'),
    )
  }
  refreshScan()
}

// Any filter change starts again at page 1 and lands in the address.
watch(
  query,
  (next, prev) => {
    if ((next.page ?? 1) === (prev?.page ?? 1) && (next.page ?? 1) !== 1)
      return void (query.value = { ...next, page: 1 })
    const params = gemParams(query.value)
    history.replaceState(null, '', `/gems${params.size ? `?${params}` : ''}`)
    load()
  },
  { deep: true },
)

// Another site or category: its own cars and fetch.
watch([source, category], (_, [before]) => {
  if (!before) return // the first pick, on mount, loads below
  load()
  refreshScan()
})

function goTo(n: number) {
  query.value = { ...query.value, page: n }
  window.scrollTo({ top: 0, behavior: 'smooth' })
}
function setSort(sort: GemSort) {
  query.value = { ...query.value, sort }
}

onMounted(async () => {
  try {
    sources.value = await api.gemSources()
  } catch {
    // The list still loads for the one source the server has.
  }
  source.value = sources.value[0]?.id ?? ''
  category.value = sources.value[0]?.categories[0] ?? 'cars'
  load()
  refreshScan()
})
onUnmounted(() => clearTimeout(timer))
</script>

<template>
  <section>
    <PageHeader :title="t('gems.title')" :description="t('gems.lead')">
      <button
        v-if="me.admin"
        class="btn-primary"
        :disabled="running"
        @click="startScan"
      >
        <RefreshCw :size="16" :class="{ 'animate-spin': running }" />
        {{ running ? t('gems.scanning') : t('gems.scan', { site: siteName }) }}
      </button>
    </PageHeader>

    <div class="mb-5 flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
      <TabBar
        v-if="sources.length"
        v-model="source"
        :tabs="sources.map((s) => ({ id: s.id, label: s.name }))"
        :label="t('gems.sites')"
        class="flex-1"
      />
      <div
        class="inline-flex rounded-md bg-gray-150/80 p-0.5"
        role="radiogroup"
        :aria-label="t('gems.category')"
      >
        <button
          v-for="c in categories"
          :key="c"
          type="button"
          role="radio"
          :aria-checked="category === c"
          class="flex h-7 items-center rounded-[5px] px-3 text-small transition-colors"
          :class="
            category === c
              ? 'bg-raised font-medium text-gray-900 shadow-card'
              : 'text-gray-600 hover:text-gray-900'
          "
          @click="category = c"
        >
          {{ tKey(`gems.categories.${c}`, c) }}
        </button>
      </div>
    </div>

    <div class="mb-4" role="status">
      <p class="text-sm text-gray-600">{{ status }}</p>
      <div
        v-if="progress !== null"
        class="mt-2 h-1.5 overflow-hidden rounded-full bg-gray-150"
        aria-hidden="true"
      >
        <div
          class="h-full rounded-full bg-brand-600 transition-[width] duration-500"
          :style="{ width: `${progress * 100}%` }"
        />
      </div>
    </div>
    <div
      v-if="me.admin && scan?.status === 'done' && scan.note"
      role="alert"
      class="message-bar-warning mb-4"
    >
      <TriangleAlert :size="16" class="mt-0.5 shrink-0 text-warning-600" />{{
        scan.note
      }}
    </div>
    <div v-if="error" role="alert" class="message-bar-danger mb-6">
      <CircleAlert :size="16" class="mt-0.5 shrink-0 text-danger-600" />{{
        error
      }}
    </div>

    <div class="grid gap-6 lg:grid-cols-[260px_minmax(0,1fr)]">
      <aside class="hidden lg:block" :aria-label="t('gems.filters')">
        <div class="card sticky top-6 p-4">
          <div class="mb-4 flex items-center justify-between">
            <h2 class="text-sm font-semibold text-gray-900">
              {{ t('gems.filters') }}
            </h2>
            <button
              v-if="chips.length"
              class="text-small text-brand-600 hover:underline"
              @click="clearAll"
            >
              {{ t('gems.clearAll') }}
            </button>
          </div>
          <GemFilters
            v-model="query"
            :facets="page?.facets ?? null"
            :detailed="page?.detailed ?? 0"
            :all="page?.all ?? 0"
          />
        </div>
      </aside>

      <div class="min-w-0">
        <div class="mb-3 flex flex-wrap items-center gap-2">
          <button class="btn-secondary lg:hidden" @click="drawer = true">
            <SlidersHorizontal :size="16" />{{ t('gems.filters') }}
            <span v-if="chips.length" class="badge-brand tabular-nums">{{
              chips.length
            }}</span>
          </button>
          <span v-if="page" class="text-sm text-gray-600 tabular-nums">
            {{ t('gems.count', { n: page.total }) }}
          </span>
          <select
            :value="query.sort ?? ''"
            class="field ml-auto w-auto"
            :aria-label="t('gems.sort')"
            @change="
              setSort(($event.target as HTMLSelectElement).value as GemSort)
            "
          >
            <option value="">{{ t('gems.sortBest') }}</option>
            <option value="discount">{{ t('gems.sortDiscount') }}</option>
            <option value="price">{{ t('gems.sortPrice') }}</option>
            <option value="year">{{ t('gems.sortYear') }}</option>
            <option value="mileage">{{ t('gems.sortMileage') }}</option>
          </select>
        </div>

        <div
          v-if="chips.length"
          class="mb-4 flex flex-wrap items-center gap-1.5"
        >
          <button
            v-for="c in chips"
            :key="c.id"
            type="button"
            class="chip"
            aria-pressed="true"
            @click="c.clear"
          >
            {{ c.label }}<X :size="12" />
          </button>
          <button
            class="ml-1 text-small text-gray-600 hover:text-gray-900 hover:underline"
            @click="clearAll"
          >
            {{ t('gems.clearAll') }}
          </button>
        </div>

        <ul v-if="loading && !page" class="space-y-3" role="status">
          <li v-for="n in 4" :key="n" class="card flex gap-4 p-4">
            <div class="skeleton h-24 w-36 rounded-md" />
            <div class="flex-1">
              <div class="skeleton h-4 w-56 max-w-full" />
              <div class="skeleton mt-2 h-3 w-40" />
              <div class="skeleton mt-4 h-8 w-72 max-w-full" />
            </div>
          </li>
        </ul>

        <div v-else-if="!page?.items.length" class="card">
          <EmptyState
            :icon="GemIcon"
            :title="t('gems.emptyTitle')"
            :description="t('gems.emptyBody')"
          >
            <button v-if="chips.length" class="btn-secondary" @click="clearAll">
              {{ t('gems.clearAll') }}
            </button>
          </EmptyState>
        </div>

        <ul
          v-else
          class="space-y-3 transition-opacity"
          :class="{ 'opacity-60': loading }"
          :aria-busy="loading"
        >
          <GemCard v-for="g in page.items" :key="g.externalId" :gem="g" />
        </ul>

        <nav
          v-if="page && pages > 1"
          class="mt-5 flex items-center justify-between text-small text-gray-600"
        >
          <button
            class="btn-secondary"
            :disabled="page.page <= 1"
            @click="goTo(page.page - 1)"
          >
            {{ t('gems.prev') }}
          </button>
          <span class="tabular-nums">{{
            t('gems.page', { page: page.page, pages })
          }}</span>
          <button
            class="btn-secondary"
            :disabled="page.page >= pages"
            @click="goTo(page.page + 1)"
          >
            {{ t('gems.next') }}
          </button>
        </nav>
      </div>
    </div>

    <BaseDialog
      v-if="drawer"
      :title="t('gems.filters')"
      light-dismiss
      @close="drawer = false"
    >
      <GemFilters
        v-model="query"
        :facets="page?.facets ?? null"
        :detailed="page?.detailed ?? 0"
        :all="page?.all ?? 0"
      />
      <template #footer>
        <button v-if="chips.length" class="btn-subtle" @click="clearAll">
          {{ t('gems.clearAll') }}
        </button>
        <button class="btn-primary" @click="drawer = false">
          {{ t('gems.showResults', { n: page?.total ?? 0 }) }}
        </button>
      </template>
    </BaseDialog>
  </section>
</template>
