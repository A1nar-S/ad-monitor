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
  parseGemParams,
  type GemPage,
  type GemQuery,
  type GemScan,
  type GemSource,
  type GemSpec,
  type Me,
} from '../api'
import { formatNumber, formatRelative, t, tKey } from '../i18n'
import { toastError } from '../toast'

// The gem finder: every listing of a category for sale on a site, rated by
// the Go side. What can be filtered and shown comes from the category's
// spec; this page sends the filters (kept in the address, so a view can be
// shared or reloaded) and renders what comes back. Admins also fetch.
const props = defineProps<{ me: Me }>()

const sources = ref<GemSource[]>([])
const source = ref('')
const category = ref('')
const scan = ref<GemScan | null>(null)
const page = ref<GemPage | null>(null)
const error = ref('')
const loading = ref(true)
const drawer = ref(false)

const empty = (): GemQuery => ({ choices: {}, ranges: {} })
const query = ref<GemQuery>(empty())

const site = computed(() => sources.value.find((s) => s.id === source.value))
const siteName = computed(() => site.value?.name ?? '')
const categories = computed(() => site.value?.categories ?? [])
const spec = computed<GemSpec>(
  () =>
    categories.value.find((c) => c.id === category.value)?.spec ?? {
      fields: [],
      sorts: [],
    },
)
const lead = computed(() =>
  tKey(`gems.leads.${category.value}`, t('gems.lead')),
)

const running = computed(
  () => scan.value?.status === 'requested' || scan.value?.status === 'running',
)
const pages = computed(() =>
  page.value
    ? Math.max(1, Math.ceil(page.value.total / page.value.pageSize))
    : 1,
)

// The page buttons: the first and last, two either side of the current
// one, and 0 for a gap ("…"). A gap of one page shows that page instead.
const pageLinks = computed(() => {
  const current = page.value?.page ?? 1
  const last = pages.value
  const shown = new Set([1, last])
  for (let n = current - 2; n <= current + 2; n++)
    if (n >= 1 && n <= last) shown.add(n)
  const sorted = [...shown].sort((a, b) => a - b)
  const out: number[] = []
  for (const n of sorted) {
    const prev = out[out.length - 1]
    if (prev !== undefined && n - prev === 2) out.push(n - 1)
    else if (prev !== undefined && n - prev > 2) out.push(0)
    out.push(n)
  }
  return out
})

const status = computed(() => {
  const s = scan.value
  const name = siteName.value
  if (!s)
    return props.me.admin
      ? t('gems.neverScannedAdmin')
      : t('gems.neverScanned', { site: name })
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

const label = (key: string) => tKey(`gems.fields.${key}`, key)
const sorts = computed(() => [
  { id: '', label: t('gems.sortBest') },
  { id: 'discount', label: t('gems.sortDiscount') },
  { id: 'price', label: t('gems.sortPrice') },
  ...spec.value.sorts.map((s) => ({
    id: s.id,
    label: tKey(`gems.sorts.${s.id}`, s.id),
  })),
])

// The filters set, as removable chips above the results.
type Chip = { id: string; label: string; clear: () => void }
const range = (name: string, lo?: number, hi?: number) =>
  lo && hi
    ? `${name}: ${formatNumber(lo)}–${formatNumber(hi)}`
    : lo
      ? `${name} ≥ ${formatNumber(lo)}`
      : `${name} ≤ ${formatNumber(hi ?? 0)}`
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
  if (q.minPrice || q.maxPrice)
    out.push({
      id: 'price',
      label: range(t('gems.price'), q.minPrice, q.maxPrice),
      clear: () => set({ minPrice: undefined, maxPrice: undefined }),
    })
  for (const f of spec.value.fields) {
    if (f.kind === 'choice')
      for (const v of q.choices[f.key] ?? [])
        out.push({
          id: `${f.key}:${v}`,
          label: tKey(`gems.values.${f.key}.${v}`, v),
          clear: () => {
            const choices = { ...q.choices }
            choices[f.key] = (q.choices[f.key] ?? []).filter((x) => x !== v)
            for (const d of spec.value.fields)
              if (d.dependsOn === f.key) delete choices[d.key]
            set({ choices })
          },
        })
    const r = q.ranges[f.key]
    if (f.kind === 'range' && (r?.min || r?.max))
      out.push({
        id: f.key,
        label: range(label(f.key), r.min, r.max),
        clear: () => {
          const ranges = { ...q.ranges }
          delete ranges[f.key]
          set({ ranges })
        },
      })
  }
  if (q.detailed)
    out.push({
      id: 'detailed',
      label: t('gems.detailedOnly'),
      clear: () => set({ detailed: false }),
    })
  return out
})

function clearAll() {
  query.value = { ...empty(), sort: query.value.sort }
}

let loadSeq = 0
async function load() {
  if (!source.value || !category.value) return
  const seq = ++loadSeq
  loading.value = true
  try {
    const result = await api.gems(source.value, category.value, query.value)
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
let lastSaved = -1
async function refreshScan() {
  if (!source.value || !category.value) return
  const wasRunning = running.value
  try {
    scan.value = await api.gemScan(source.value, category.value)
  } catch {
    // A missed poll is retried on the next tick.
  }
  const s = scan.value
  // Pass 1's results land at once and pass 2 saves every 100 ad pages:
  // reload as they come, and when the fetch ends.
  if (s?.phase === 'detailing' && Math.floor(s.detailed / 100) !== lastSaved) {
    if (lastSaved !== -1) load()
    lastSaved = Math.floor(s.detailed / 100)
  }
  if (wasRunning && !running.value) load()
  clearTimeout(timer)
  // Fast while a fetch runs; slowly otherwise, to notice one an admin starts.
  timer = setTimeout(refreshScan, running.value ? 5000 : 60000)
}

async function startScan() {
  try {
    scan.value = await api.requestGemScan(source.value, category.value)
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

// The address: the site, the category and the query.
function remember() {
  const params = gemParams(query.value)
  params.set('source', source.value)
  params.set('category', category.value)
  history.replaceState(null, '', `/gems?${params}`)
}

// Any filter change starts again at page 1 and lands in the address.
watch(
  query,
  (next, prev) => {
    if ((next.page ?? 1) === (prev?.page ?? 1) && (next.page ?? 1) !== 1)
      return void (query.value = { ...next, page: 1 })
    remember()
    load()
  },
  { deep: true },
)

// Another site or category: its own filters, gems and fetch.
function pick(src: string, cat: string) {
  source.value = src
  category.value = cat
  page.value = null
  lastSaved = -1
  query.value = empty() // the watch above saves and loads
  refreshScan()
}
watch(source, (next, before) => {
  if (before && next !== before) pick(next, site.value?.categories[0]?.id ?? '')
})

function goTo(n: number) {
  query.value = { ...query.value, page: n }
  window.scrollTo({ top: 0, behavior: 'smooth' })
}
function setSort(sort: string) {
  query.value = { ...query.value, sort }
}

onMounted(async () => {
  try {
    sources.value = await api.gemSources()
  } catch (reason) {
    error.value =
      reason instanceof ApiError ? reason.message : t('gems.loadFailed')
    loading.value = false
    return
  }
  const p = new URLSearchParams(location.search)
  const s =
    sources.value.find((x) => x.id === p.get('source')) ?? sources.value[0]
  const c =
    s?.categories.find((x) => x.id === p.get('category')) ?? s?.categories[0]
  if (!s || !c) {
    loading.value = false
    return
  }
  source.value = s.id
  category.value = c.id
  query.value = parseGemParams(p, c.spec) // the watch loads
  refreshScan()
})
onUnmounted(() => clearTimeout(timer))
</script>

<template>
  <section>
    <PageHeader :title="t('gems.title')" :description="lead">
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
          v-for="c in categories.map((x) => x.id)"
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
          @click="c !== category && pick(source, c)"
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
        <div
          class="card sticky top-6 max-h-[calc(100dvh-3rem)] overflow-y-auto overscroll-contain p-4"
        >
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
            :spec="spec"
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
            @change="setSort(($event.target as HTMLSelectElement).value)"
          >
            <option v-for="o in sorts" :key="o.id" :value="o.id">
              {{ o.label }}
            </option>
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
          <GemCard
            v-for="g in page.items"
            :key="g.externalId"
            :gem="g"
            :spec="spec"
            :category="category"
          />
        </ul>

        <nav
          v-if="page && pages > 1"
          class="mt-5 flex flex-wrap items-center justify-center gap-1 text-small"
          :aria-label="t('gems.pagination')"
        >
          <button
            class="btn-subtle"
            :disabled="page.page <= 1"
            @click="goTo(page.page - 1)"
          >
            {{ t('gems.prev') }}
          </button>
          <template v-for="(n, i) in pageLinks" :key="i">
            <span v-if="n === 0" class="px-1 text-gray-400" aria-hidden="true"
              >…</span
            >
            <button
              v-else
              class="h-8 min-w-8 rounded-md px-2 tabular-nums transition-colors"
              :class="
                n === page.page
                  ? 'bg-brand-600 font-medium text-white'
                  : 'text-gray-700 hover:bg-gray-100'
              "
              :aria-current="n === page.page ? 'page' : undefined"
              :aria-label="t('gems.page', { page: n, pages })"
              @click="n !== page.page && goTo(n)"
            >
              {{ formatNumber(n) }}
            </button>
          </template>
          <button
            class="btn-subtle"
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
        :spec="spec"
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
