<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  ArrowUpRight,
  ChevronRight,
  CircleAlert,
  Database,
  ImageOff,
  TriangleAlert,
  Layers3,
  RefreshCw,
  Search,
  SlidersHorizontal,
  X,
} from 'lucide-vue-next'
import BaseDialog from './BaseDialog.vue'
import EmptyState from './EmptyState.vue'
import OverviewFilters from './OverviewFilters.vue'
import PageHeader from './PageHeader.vue'
import PageNav from './PageNav.vue'
import TabBar from './TabBar.vue'
import { ApiError, readListingDetails } from '../api'
import { toast, toastError } from '../toast'
import { categoryName, formatDate, formatNumber, t, tKey } from '../i18n'

type Listing = {
  id: number
  source: string
  title: string
  url: string
  location: string
  price: string
  imageUrl: string
  active: boolean
  firstSeenAt: string
  attrs: Record<string, unknown>
  // When the ad's own page was last read (region, gearbox, ...); null = never.
  detailedAt?: string | null
  category: 'cars' | 'flats' | 'parts' | null
  hasAdPage: boolean // the server can read this ad's page for details
}
type SavedSearch = {
  id: number
  name: string
  userName: string
  source: string
  enabled: boolean
  feeds: string[]
  filters: Record<string, unknown>
  channel: string
  matches: number
}
type Overview = {
  updatedAt: string
  stats: {
    listings: number
    activeListings: number
    newToday: number
    searches: number
    enabledSearches: number
    users: number
    matches: number
    deliveries: number
  }
  // One page of the asked view, searched, filtered and sorted by the server.
  view: 'listings' | 'searches'
  sources: string[]
  total: number
  page: number
  pages: number
  pageSize: number
  items: (Listing | SavedSearch)[]
}

const data = ref<Overview | null>(null)
const loading = ref(false)
const error = ref('')
const view = ref<'listings' | 'searches'>('listings')
const query = ref('')
const source = ref('all')
const category = ref<'all' | 'cars' | 'flats' | 'parts'>('all')
const categoryOptions = [
  { value: 'all', label: () => t('overview.all') },
  { value: 'cars', label: () => categoryName('cars') },
  { value: 'flats', label: () => categoryName('flats') },
  { value: 'parts', label: () => categoryName('parts') },
] as const
const status = ref('all')
const sort = ref('newest')
const page = ref(1)
const openListing = ref<Listing | null>(null)
const openSearch = ref<SavedSearch | null>(null)
const failedImages = ref(new Set<number>())
const refreshing = ref(false) // a refresh asked for with the button
const drawer = ref(false) // the filters, on narrow screens
let timer: ReturnType<typeof setInterval> | undefined
let typing: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
// The button's "updated" toast is owed until a request finishes, even when a
// filter change or the 30-second tick replaced the one the button started.
let owesToast = false
let gone = false // left the page: nothing to say any more

// Asks the server for the current page; a newer request cancels an older one.
// The 30-second refresh is quiet; the button dims the page and says when done.
async function refresh(manual = false) {
  controller?.abort()
  const mine = new AbortController()
  controller = mine
  loading.value = true
  if (manual) owesToast = refreshing.value = true
  // Long enough to see that the page reloaded, even when it was instant.
  const shown = new Promise((done) => setTimeout(done, owesToast ? 400 : 0))
  const timeout = setTimeout(() => mine.abort(), 10000)
  const params = new URLSearchParams({
    view: view.value,
    sort: sort.value,
    page: String(page.value),
  })
  if (query.value.trim()) params.set('q', query.value.trim())
  if (source.value !== 'all') params.set('source', source.value)
  if (status.value !== 'all') params.set('status', status.value)
  if (view.value === 'listings' && category.value !== 'all')
    params.set('category', category.value)
  try {
    const response = await fetch(`/api/overview?${params}`, {
      signal: mine.signal,
    })
    if (response.status === 403) throw new Error(t('overview.adminsOnly'))
    if (!response.ok) throw new Error(t('overview.dbDown'))
    const next: Overview = await response.json()
    await shown
    if (controller !== mine || gone) return
    data.value = next
    page.value = next.page // the server clamps past-the-end pages
    error.value = ''
    if (owesToast) toast(t('toast.refreshed'))
    owesToast = false
  } catch (reason) {
    if (controller !== mine || gone) return
    error.value =
      reason instanceof Error && reason.name !== 'AbortError'
        ? reason.message
        : t('overview.loadFailed')
    if (owesToast) toastError(`${t('toast.refreshFailed')}: ${error.value}`)
    owesToast = false
  } finally {
    clearTimeout(timeout)
    if (controller === mine) loading.value = refreshing.value = false
  }
}

// The page on screen; empty while another view's page is still shown.
const current = computed(() =>
  data.value?.view === view.value ? data.value : null,
)
const sources = computed(() => current.value?.sources ?? [])
const visibleListings = computed(() =>
  view.value === 'listings' ? ((current.value?.items ?? []) as Listing[]) : [],
)
const visibleSearches = computed(() =>
  view.value === 'searches'
    ? ((current.value?.items ?? []) as SavedSearch[])
    : [],
)
const resultCount = computed(() => current.value?.total ?? 0)
const pageCount = computed(() => current.value?.pages ?? 1)
const pageSize = computed(() => current.value?.pageSize ?? 20)
// Anything collected at all in this view, filters aside.
const hasRecords = computed(
  () =>
    (view.value === 'listings'
      ? data.value?.stats.listings
      : data.value?.stats.searches) !== 0,
)
const hasFilters = computed(
  () =>
    query.value !== '' ||
    source.value !== 'all' ||
    status.value !== 'all' ||
    category.value !== 'all',
)
const metrics = computed(() => {
  const stats = data.value?.stats
  return [
    {
      name: t('overview.activeListings'),
      value: stats?.activeListings,
      detail: t('overview.ofCollected', { n: stats?.listings ?? 0 }),
    },
    {
      name: t('overview.newToday'),
      value: stats?.newToday,
      detail: t('overview.sinceMidnight'),
    },
    {
      name: t('overview.searchMatches'),
      value: stats?.matches,
      detail: t('overview.searchesActive', {
        active: stats?.enabledSearches ?? 0,
        total: stats?.searches ?? 0,
      }),
    },
    {
      name: t('overview.notificationsSent'),
      value: stats?.deliveries,
      detail: t('overview.acrossUsers', { n: stats?.users ?? 0 }),
    },
  ]
})
const updatedAt = computed(() =>
  data.value
    ? formatDate(data.value.updatedAt, {
        hour: '2-digit',
        minute: '2-digit',
      })
    : '',
)
// A changed filter starts again at page 1; typing waits for a pause.
watch([source, category, status, sort, view], () => {
  page.value = 1
  void refresh()
})
watch(query, () => {
  clearTimeout(typing)
  typing = setTimeout(() => {
    page.value = 1
    void refresh()
  }, 250)
})
function goTo(next: number) {
  page.value = next
  void refresh()
}

// The order specifications are shown in; anything else follows.
const attributeOrder = [
  'make',
  'model',
  'year',
  'mileage_km',
  'engine_l',
  'fuel',
  'gearbox',
  'body',
  'color',
  'seats',
  'region',
  'inspection',
  'rooms',
  'area_m2',
  'floor',
  'floors',
  'series',
  'house_type',
  'district',
  'address',
]
const humanize = (key: string) =>
  tKey(
    `attrs.${key}`,
    key.replace(/_/g, ' ').replace(/^./, (letter) => letter.toUpperCase()),
  )
// Shown in their own sections, not as specifications.
const ownSection = new Set(['text', 'description', 'equipment'])
const rank = (key: string) =>
  attributeOrder.includes(key) ? attributeOrder.indexOf(key) : 999

const listingAttributes = computed(() =>
  Object.entries(openListing.value?.attrs ?? {})
    .filter(
      ([key, value]) => !ownSection.has(key) && value !== null && value !== '',
    )
    .sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
    .map(([key, value]) => ({
      key,
      label: humanize(key),
      value: formatAttribute(key, value),
    })),
)
const equipment = computed(() => {
  const value = openListing.value?.attrs?.equipment
  return Array.isArray(value) ? value.map(String) : []
})
// The ad's own text: "text" from the ad page (older rows: "description").
const description = computed(() => {
  const attrs = openListing.value?.attrs ?? {}
  const value = attrs.text ?? attrs.description
  return typeof value === 'string' && value.trim() ? value : null
})

// Reading the open listing's ad page on ss.com, which adds what the list
// page lacks (a car's region, gearbox, ...). Done once per ad, then saved.
const details = ref<'idle' | 'loading' | 'failed' | 'gone'>('idle')
const detailsError = ref('')

async function loadDetails(listing: Listing) {
  details.value = 'loading'
  detailsError.value = ''
  try {
    const read = await readListingDetails(listing.id)
    listing.attrs = read.attrs
    listing.active = read.active
    listing.detailedAt = read.detailedAt
    if (openListing.value?.id === listing.id) details.value = 'idle'
  } catch (reason) {
    if (openListing.value?.id !== listing.id) return
    if (reason instanceof ApiError && reason.status === 410) {
      listing.active = false
      details.value = 'gone'
      return
    }
    details.value = 'failed'
    detailsError.value =
      reason instanceof ApiError ? reason.message : t('overview.readFailed')
  }
}

watch(
  () => openListing.value?.id,
  () => {
    details.value = 'idle'
    const l = openListing.value
    if (l && !l.detailedAt && l.hasAdPage) void loadDetails(l)
  },
)

function formatAttribute(key: string, value: unknown): string {
  if (typeof value === 'number') {
    const formatted = formatNumber(value, {
      useGrouping: key !== 'year',
      maximumFractionDigits: 2,
      minimumFractionDigits: key === 'engine_l' ? 1 : 0,
    })
    return `${formatted}${({ mileage_km: ' km', engine_l: ' L', area_m2: ' m²' } as Record<string, string>)[key] ?? ''}`
  }
  if (key === 'fuel' && typeof value === 'string')
    return tKey(`fuelCodes.${value}`, value)
  if (typeof value === 'boolean')
    return value ? t('common.yes') : t('common.no')
  if (Array.isArray(value)) return value.join(', ')
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

// A saved search's filters as readable rows: lists joined, ranges as a–b.
// Stored searches hold them compiled ({ranges: {...}, one_of: {...}}), so
// those groups are opened up first.
const compiledGroups = ['ranges', 'one_of', 'all_of', 'contains']
function filterRows(filters: Record<string, unknown>) {
  const flat: [string, unknown][] = []
  for (const [key, value] of Object.entries(filters ?? {})) {
    if (compiledGroups.includes(key) && value && typeof value === 'object')
      flat.push(...Object.entries(value as Record<string, unknown>))
    else flat.push([key, value])
  }
  return flat.map(([key, value]) => {
    const n = (v: number) => formatNumber(v, { useGrouping: key !== 'year' })
    let text: string
    if (Array.isArray(value)) text = value.join(', ')
    else if (value && typeof value === 'object') {
      const { min, max } = value as { min?: number; max?: number }
      text =
        min !== undefined && max !== undefined
          ? `${n(min)} – ${n(max)}`
          : min !== undefined
            ? t('overview.orMore', { n: n(min) })
            : max !== undefined
              ? t('overview.upTo', { n: n(max) })
              : JSON.stringify(value)
    } else text = String(value)
    return { key, label: humanize(key), value: text }
  })
}

function safeURL(value: string) {
  try {
    const url = new URL(value)
    return ['https:', 'http:'].includes(url.protocol) ? url.href : undefined
  } catch {
    return undefined
  }
}
function formatTime(value: string) {
  return formatDate(value, { dateStyle: 'medium', timeStyle: 'short' })
}
// The filters set, as removable chips above the results.
const filterChips = computed(() => {
  const out: { id: string; label: string; clear: () => void }[] = []
  if (query.value)
    out.push({
      id: 'q',
      label: `“${query.value}”`,
      clear: () => (query.value = ''),
    })
  if (view.value === 'listings' && category.value !== 'all')
    out.push({
      id: 'category',
      label: categoryName(category.value),
      clear: () => (category.value = 'all'),
    })
  if (status.value !== 'all')
    out.push({
      id: 'status',
      label:
        status.value === 'active'
          ? view.value === 'listings'
            ? t('common.active')
            : t('common.enabled')
          : view.value === 'listings'
            ? t('common.inactive')
            : t('common.paused'),
      clear: () => (status.value = 'all'),
    })
  if (source.value !== 'all')
    out.push({
      id: 'source',
      label: source.value,
      clear: () => (source.value = 'all'),
    })
  return out
})

// A few telling specs for a listing's row, as the gem finder's cards show.
const rowSpecs = ['year', 'mileage_km', 'fuel', 'rooms', 'area_m2', 'floor']
function quickSpecs(listing: Listing) {
  return rowSpecs
    .filter((key) => {
      const v = listing.attrs?.[key]
      return v !== undefined && v !== null && v !== ''
    })
    .slice(0, 4)
    .map((key) => formatAttribute(key, listing.attrs[key]))
}

function resetFilters() {
  query.value = ''
  source.value = 'all'
  category.value = 'all'
  status.value = 'all'
}
function changeView(next: 'listings' | 'searches') {
  view.value = next
  resetFilters()
}

onMounted(() => {
  void refresh()
  timer = setInterval(() => void refresh(), 30000)
})
onUnmounted(() => {
  gone = true
  clearInterval(timer)
  clearTimeout(typing)
  controller?.abort()
})
</script>

<template>
  <section>
    <PageHeader :title="t('overview.title')" :description="t('overview.lead')">
      <span
        v-if="data"
        class="mr-2 hidden text-small text-gray-500 tabular-nums sm:inline"
        >{{ t('overview.updated', { time: updatedAt }) }}</span
      >
      <button
        class="btn-secondary"
        :disabled="refreshing"
        @click="refresh(true)"
      >
        <RefreshCw :size="14" :class="{ 'animate-spin': refreshing }" />{{
          t('overview.refresh')
        }}
      </button>
    </PageHeader>

    <div v-if="error" role="alert" class="message-bar-danger mb-6">
      <CircleAlert :size="16" class="mt-0.5 shrink-0 text-danger-600" />
      <span class="flex-1"
        >{{ error
        }}<template v-if="data"> {{ t('overview.staleData') }}</template></span
      >
    </div>

    <section
      :aria-label="t('overview.totals')"
      class="grid grid-cols-2 gap-px overflow-hidden rounded-lg bg-gray-150 shadow-card transition-opacity duration-150 md:grid-cols-4"
      :class="{ 'opacity-50': refreshing }"
    >
      <div v-for="metric in metrics" :key="metric.name" class="bg-surface p-5">
        <p class="text-small text-gray-600">{{ metric.name }}</p>
        <p
          v-if="metric.value !== undefined"
          class="mt-1 font-display text-display font-semibold tracking-[-0.015em] text-gray-900 tabular-nums"
        >
          {{ formatNumber(metric.value) }}
        </p>
        <div v-else class="skeleton mt-2 mb-1 h-7 w-16" />
        <p class="mt-1 truncate text-xs text-gray-500">{{ metric.detail }}</p>
      </div>
    </section>

    <TabBar
      class="mt-10"
      :model-value="view"
      :label="t('overview.records')"
      :tabs="[
        {
          id: 'listings',
          label: t('overview.tabListings'),
          count: data?.stats.listings,
        },
        {
          id: 'searches',
          label: t('overview.tabSearches'),
          count: data?.stats.searches,
        },
      ]"
      @update:model-value="changeView"
    />

    <div class="mt-5 grid gap-6 lg:grid-cols-[260px_minmax(0,1fr)]">
      <aside
        v-if="hasRecords"
        class="hidden lg:block"
        :aria-label="t('common.filters')"
      >
        <div
          class="card sticky top-6 max-h-[calc(100dvh-3rem)] overflow-y-auto overscroll-contain p-4"
        >
          <div class="mb-4 flex items-center justify-between">
            <h2 class="text-sm font-semibold text-gray-900">
              {{ t('common.filters') }}
            </h2>
            <button
              v-if="hasFilters"
              class="text-small text-brand-600 hover:underline"
              @click="resetFilters"
            >
              {{ t('common.clearAll') }}
            </button>
          </div>
          <OverviewFilters
            v-model:query="query"
            v-model:category="category"
            v-model:status="status"
            v-model:source="source"
            :view="view"
            :sources="sources"
          />
        </div>
      </aside>

      <div class="min-w-0" :class="{ 'lg:col-span-2': !hasRecords }">
        <div v-if="hasRecords" class="mb-3 flex flex-wrap items-center gap-2">
          <button class="btn-secondary lg:hidden" @click="drawer = true">
            <SlidersHorizontal :size="16" />{{ t('common.filters') }}
            <span v-if="filterChips.length" class="badge-brand tabular-nums">{{
              filterChips.length
            }}</span>
          </button>
          <span v-if="current" class="text-sm text-gray-600 tabular-nums">
            {{
              t('overview.range', {
                from: resultCount ? (page - 1) * pageSize + 1 : 0,
                to: Math.min(page * pageSize, resultCount),
                total: resultCount,
              })
            }}
          </span>
          <select
            v-if="view === 'listings'"
            v-model="sort"
            class="field ml-auto w-auto"
            :aria-label="t('overview.sort')"
          >
            <option value="newest">{{ t('overview.newest') }}</option>
            <option value="oldest">{{ t('overview.oldest') }}</option>
            <option value="title">{{ t('overview.byTitle') }}</option>
          </select>
        </div>

        <div
          v-if="filterChips.length"
          class="mb-4 flex flex-wrap items-center gap-1.5"
        >
          <button
            v-for="c in filterChips"
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
            @click="resetFilters"
          >
            {{ t('common.clearAll') }}
          </button>
        </div>

        <ul
          v-if="!current && loading"
          class="space-y-3"
          role="status"
          :aria-label="t('common.loading')"
        >
          <li v-for="n in 5" :key="n" class="card flex gap-3 p-4">
            <div class="skeleton h-15 w-20 rounded-md" />
            <div class="flex-1">
              <div class="skeleton h-4 w-80 max-w-full" />
              <div class="skeleton mt-2 h-3 w-40" />
              <div class="skeleton mt-3 h-5 w-56 max-w-full" />
            </div>
          </li>
        </ul>

        <div v-else-if="!current && error" class="card">
          <EmptyState
            :icon="Database"
            :title="t('overview.unavailableTitle')"
            :description="t('overview.unavailableBody')"
          >
            <button class="btn-secondary" @click="refresh(true)">
              <RefreshCw :size="14" />{{ t('common.tryAgain') }}
            </button>
          </EmptyState>
        </div>

        <div v-else-if="resultCount === 0" class="card">
          <EmptyState
            :icon="
              hasFilters
                ? Search
                : view === 'listings'
                  ? Layers3
                  : SlidersHorizontal
            "
            :title="
              hasFilters
                ? t('overview.noMatchTitle')
                : view === 'listings'
                  ? t('overview.noListingsTitle')
                  : t('overview.noSearchesTitle')
            "
            :description="
              hasFilters
                ? t('overview.noMatchBody')
                : view === 'listings'
                  ? t('overview.noListingsBody')
                  : t('overview.noSearchesBody')
            "
          >
            <button
              v-if="hasFilters"
              class="btn-secondary"
              @click="resetFilters"
            >
              {{ t('overview.clearFilters') }}
            </button>
          </EmptyState>
        </div>

        <ul
          v-else-if="view === 'listings'"
          class="space-y-3 transition-opacity duration-150"
          :class="{ 'opacity-60': loading }"
          :aria-busy="loading"
        >
          <li
            v-for="listing in visibleListings"
            :key="listing.id"
            class="card group cursor-pointer p-4 transition-shadow hover:shadow-raised"
            @click="openListing = listing"
          >
            <div class="flex items-start gap-3">
              <div
                class="grid h-15 w-20 shrink-0 place-items-center overflow-hidden rounded-md bg-gray-100 text-gray-400"
              >
                <img
                  v-if="
                    safeURL(listing.imageUrl) && !failedImages.has(listing.id)
                  "
                  :src="safeURL(listing.imageUrl)"
                  alt=""
                  class="size-full object-cover"
                  loading="lazy"
                  referrerpolicy="no-referrer"
                  @error="failedImages.add(listing.id)"
                /><ImageOff v-else :size="16" />
              </div>
              <div class="min-w-0 flex-1">
                <button
                  class="block max-w-full truncate text-left text-base font-semibold text-gray-900 group-hover:text-brand-600"
                  @click.stop="openListing = listing"
                >
                  {{ listing.title }}
                </button>
                <p class="truncate text-small text-gray-500">
                  {{ listing.source }} ·
                  {{ listing.location || t('overview.noCategory') }}
                </p>
              </div>
              <div class="shrink-0 text-right">
                <p class="text-base font-semibold text-gray-900 tabular-nums">
                  {{ listing.price || '—' }}
                </p>
                <p class="text-small text-gray-500 tabular-nums">
                  {{ formatTime(listing.firstSeenAt) }}
                </p>
              </div>
            </div>
            <ul class="mt-2 flex flex-wrap gap-1.5 text-small text-gray-700">
              <li>
                <span
                  :class="listing.active ? 'badge-success' : 'badge-neutral'"
                  >{{
                    listing.active ? t('common.active') : t('common.inactive')
                  }}</span
                >
              </li>
              <li
                v-if="listing.category"
                class="rounded bg-gray-100 px-1.5 py-0.5"
              >
                {{ categoryName(listing.category) }}
              </li>
              <li
                v-for="spec in quickSpecs(listing)"
                :key="spec"
                class="rounded bg-gray-100 px-1.5 py-0.5 tabular-nums"
              >
                {{ spec }}
              </li>
            </ul>
          </li>
        </ul>

        <ul
          v-else
          class="space-y-3 transition-opacity duration-150"
          :class="{ 'opacity-60': loading }"
          :aria-busy="loading"
        >
          <li
            v-for="saved in visibleSearches"
            :key="saved.id"
            class="card group cursor-pointer p-4 transition-shadow hover:shadow-raised"
            @click="openSearch = saved"
          >
            <div class="flex items-start gap-3">
              <div class="min-w-0 flex-1">
                <button
                  class="block max-w-full truncate text-left text-base font-semibold text-gray-900 group-hover:text-brand-600"
                  @click.stop="openSearch = saved"
                >
                  {{ saved.name }}
                </button>
                <p class="truncate text-small text-gray-500">
                  {{ saved.userName }} · {{ saved.source }}
                </p>
              </div>
              <div class="shrink-0 text-right">
                <p class="text-base font-semibold text-gray-900 tabular-nums">
                  {{ formatNumber(saved.matches) }}
                </p>
                <p class="text-small text-gray-500">
                  {{ t('overview.colMatches') }}
                </p>
              </div>
            </div>
            <ul class="mt-2 flex flex-wrap gap-1.5 text-small text-gray-700">
              <li>
                <span
                  :class="saved.enabled ? 'badge-success' : 'badge-warning'"
                  >{{
                    saved.enabled ? t('common.enabled') : t('common.paused')
                  }}</span
                >
              </li>
              <li
                v-if="saved.channel"
                class="max-w-64 truncate rounded bg-gray-100 px-1.5 py-0.5"
              >
                {{ t('overview.colSendsTo') }}: {{ saved.channel }}
              </li>
            </ul>
          </li>
        </ul>

        <PageNav
          v-if="current"
          class="mt-5"
          :page="page"
          :pages="pageCount"
          @go="goTo"
        />
        <p class="mt-3 text-xs text-gray-500">
          {{ t('overview.footnote') }}
        </p>
      </div>
    </div>

    <BaseDialog
      v-if="drawer"
      :title="t('common.filters')"
      light-dismiss
      @close="drawer = false"
    >
      <OverviewFilters
        v-model:query="query"
        v-model:category="category"
        v-model:status="status"
        v-model:source="source"
        :view="view"
        :sources="sources"
      />
      <template #footer>
        <button v-if="hasFilters" class="btn-subtle" @click="resetFilters">
          {{ t('common.clearAll') }}
        </button>
        <button class="btn-primary" @click="drawer = false">
          {{ t('common.showResults', { n: resultCount }) }}
        </button>
      </template>
    </BaseDialog>

    <BaseDialog
      v-if="openListing"
      :title="t('overview.listing')"
      :subtitle="`#${openListing.id} · ${openListing.source}`"
      light-dismiss
      @close="openListing = null"
    >
      <div
        class="grid aspect-[16/10] place-items-center overflow-hidden rounded-lg bg-gray-100 text-gray-400"
      >
        <img
          v-if="
            safeURL(openListing.imageUrl) && !failedImages.has(openListing.id)
          "
          :src="safeURL(openListing.imageUrl)"
          :alt="openListing.title"
          class="size-full object-contain"
          referrerpolicy="no-referrer"
          @error="failedImages.add(openListing.id)"
        /><ImageOff v-else :size="24" />
      </div>

      <div class="mt-5 flex items-start justify-between gap-4">
        <p
          class="font-display text-display font-semibold tracking-[-0.015em] tabular-nums"
        >
          {{ openListing.price || t('overview.noPrice') }}
        </p>
        <span
          class="mt-2"
          :class="openListing.active ? 'badge-success' : 'badge-neutral'"
          >{{
            openListing.active ? t('common.active') : t('common.inactive')
          }}</span
        >
      </div>
      <p class="mt-1 text-sm text-gray-700">{{ openListing.title }}</p>
      <a
        v-if="safeURL(openListing.url)"
        :href="safeURL(openListing.url)"
        target="_blank"
        rel="noopener noreferrer"
        class="btn-primary mt-5 w-full"
        >{{ t('overview.openOn', { source: openListing.source })
        }}<ArrowUpRight :size="16"
      /></a>

      <div
        v-if="details === 'gone'"
        role="status"
        class="message-bar-warning mt-6"
      >
        <TriangleAlert :size="16" class="mt-0.5 shrink-0 text-warning-600" />
        {{ t('overview.gone', { source: openListing.source }) }}
      </div>

      <section class="mt-8" :aria-busy="details === 'loading'">
        <div class="mb-3 flex min-h-6 items-center justify-between gap-3">
          <h3 class="text-sm font-semibold text-gray-900">
            {{ t('overview.specs') }}
          </h3>
          <span
            v-if="details === 'loading'"
            class="flex items-center gap-2 text-xs text-gray-500"
            role="status"
            ><span
              class="size-3.5 animate-spin rounded-full border-2 border-gray-200 border-t-brand-600"
            />{{ t('overview.readingAdPage') }}</span
          >
          <span
            v-else-if="openListing.detailedAt && openListing.hasAdPage"
            class="flex items-center gap-1 text-xs text-gray-500"
            >{{
              t('overview.fromAdPage', {
                time: formatTime(openListing.detailedAt),
              })
            }}
            <button
              class="btn-icon size-6"
              :aria-label="t('overview.readAgain')"
              :title="t('overview.readAgain')"
              @click="loadDetails(openListing)"
            >
              <RefreshCw :size="13" /></button
          ></span>
        </div>
        <div
          v-if="details === 'failed'"
          role="alert"
          class="message-bar-danger mb-4"
        >
          <CircleAlert :size="16" class="mt-0.5 shrink-0 text-danger-600" />
          <span class="flex-1"
            >{{ detailsError }} {{ t('overview.listPageShown') }}</span
          >
          <button
            class="shrink-0 text-small font-medium text-danger-700 hover:underline"
            @click="loadDetails(openListing)"
          >
            {{ t('common.tryAgain') }}
          </button>
        </div>
        <dl
          v-if="listingAttributes.length"
          class="grid grid-cols-2 gap-x-6 gap-y-4"
        >
          <div v-for="attribute in listingAttributes" :key="attribute.key">
            <dt class="text-xs text-gray-500">{{ attribute.label }}</dt>
            <dd
              class="mt-0.5 text-sm text-gray-900 tabular-nums [overflow-wrap:anywhere]"
            >
              {{ attribute.value }}
            </dd>
          </div>
          <template v-if="details === 'loading'">
            <div v-for="n in 4" :key="n">
              <div class="skeleton h-3 w-16" />
              <div class="skeleton mt-1.5 h-4 w-24" />
            </div>
          </template>
        </dl>
        <p v-else class="text-sm text-gray-500">
          {{ t('overview.noSpecs') }}
        </p>
      </section>

      <section v-if="equipment.length" class="mt-8">
        <h3 class="mb-3 text-sm font-semibold text-gray-900">
          {{ t('overview.equipment') }}
        </h3>
        <ul class="flex flex-wrap gap-1.5">
          <li
            v-for="item in equipment"
            :key="item"
            class="rounded-md bg-gray-100 px-2 py-1 text-small text-gray-800"
          >
            {{ item }}
          </li>
        </ul>
      </section>

      <section class="mt-8">
        <h3 class="mb-2 text-sm font-semibold text-gray-900">
          {{ t('overview.description') }}
        </h3>
        <p
          v-if="description"
          class="text-sm leading-6 whitespace-pre-wrap text-gray-700 [overflow-wrap:anywhere]"
        >
          {{ description }}
        </p>
        <div v-else-if="details === 'loading'" class="grid gap-2">
          <div class="skeleton h-3.5 w-full" />
          <div class="skeleton h-3.5 w-11/12" />
          <div class="skeleton h-3.5 w-2/3" />
        </div>
        <p v-else class="text-sm text-gray-500">
          {{
            openListing.detailedAt
              ? t('overview.noDescription')
              : t('overview.notReadYet')
          }}
        </p>
      </section>

      <section class="mt-8 border-t border-gray-150 pt-6">
        <dl class="grid gap-3 text-sm">
          <div class="flex gap-4">
            <dt class="w-32 shrink-0 text-gray-500">
              {{ t('overview.firstSeen') }}
            </dt>
            <dd class="text-gray-900 tabular-nums">
              {{ formatTime(openListing.firstSeenAt) }}
            </dd>
          </div>
          <div class="flex gap-4">
            <dt class="w-32 shrink-0 text-gray-500">
              {{ t('overview.categoryPath') }}
            </dt>
            <dd
              class="min-w-0 font-mono text-xs leading-5 text-gray-700 [overflow-wrap:anywhere]"
            >
              {{ openListing.location || '—' }}
            </dd>
          </div>
        </dl>
        <details class="group mt-5">
          <summary
            class="flex list-none items-center gap-1 text-small font-medium text-gray-700 hover:text-gray-900 [&::-webkit-details-marker]:hidden"
          >
            <ChevronRight
              :size="14"
              class="transition-transform group-open:rotate-90"
            />{{ t('overview.rawAttributes') }}
          </summary>
          <pre
            class="mt-3 max-h-72 overflow-auto rounded-md bg-gray-50 p-3 font-mono text-xs leading-5 whitespace-pre-wrap text-gray-700 [overflow-wrap:anywhere]"
            >{{ JSON.stringify(openListing.attrs ?? {}, null, 2) }}</pre>
        </details>
      </section>
    </BaseDialog>

    <BaseDialog
      v-if="openSearch"
      :title="openSearch.name"
      :subtitle="t('overview.createdBy', { name: openSearch.userName })"
      light-dismiss
      @close="openSearch = null"
    >
      <dl class="grid gap-3 text-sm">
        <div class="flex gap-4">
          <dt class="w-32 shrink-0 text-gray-500">
            {{ t('overview.colStatus') }}
          </dt>
          <dd>
            <span
              :class="openSearch.enabled ? 'badge-success' : 'badge-warning'"
              >{{
                openSearch.enabled ? t('common.enabled') : t('common.paused')
              }}</span
            >
          </dd>
        </div>
        <div class="flex gap-4">
          <dt class="w-32 shrink-0 text-gray-500">
            {{ t('overview.matches') }}
          </dt>
          <dd class="text-gray-900 tabular-nums">
            {{ formatNumber(openSearch.matches) }}
          </dd>
        </div>
        <div class="flex gap-4">
          <dt class="w-32 shrink-0 text-gray-500">
            {{ t('overview.source') }}
          </dt>
          <dd class="text-gray-900">{{ openSearch.source }}</dd>
        </div>
        <div class="flex gap-4">
          <dt class="w-32 shrink-0 text-gray-500">
            {{ t('overview.sendsTo') }}
          </dt>
          <dd class="min-w-0 text-gray-900 [overflow-wrap:anywhere]">
            {{ openSearch.channel || '—' }}
          </dd>
        </div>
      </dl>

      <section class="mt-8">
        <h3 class="mb-3 text-sm font-semibold text-gray-900">
          {{ t('overview.filters') }}
        </h3>
        <dl
          v-if="filterRows(openSearch.filters).length"
          class="divide-y divide-gray-100 rounded-md shadow-card"
        >
          <div
            v-for="row in filterRows(openSearch.filters)"
            :key="row.key"
            class="flex gap-4 px-3 py-2.5 text-sm"
          >
            <dt class="w-32 shrink-0 text-gray-500">{{ row.label }}</dt>
            <dd class="min-w-0 text-gray-900 [overflow-wrap:anywhere]">
              {{ row.value }}
            </dd>
          </div>
        </dl>
        <p v-else class="text-sm text-gray-500">
          {{ t('overview.noFilters') }}
        </p>
      </section>

      <section class="mt-8">
        <h3 class="mb-1 text-sm font-semibold text-gray-900">
          {{ t('overview.feeds') }}
        </h3>
        <p class="mb-3 text-small text-gray-500">
          {{ t('overview.feedsLead') }}
        </p>
        <ul v-if="openSearch.feeds.length" class="grid gap-1.5">
          <li
            v-for="feed in openSearch.feeds"
            :key="feed"
            class="rounded-md bg-gray-50 px-3 py-2 font-mono text-xs text-gray-700 [overflow-wrap:anywhere]"
          >
            {{ feed }}
          </li>
        </ul>
        <p v-else class="text-sm text-gray-500">{{ t('overview.noFeeds') }}</p>
      </section>
    </BaseDialog>
  </section>
</template>
