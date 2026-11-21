<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  ArrowUpRight,
  CircleAlert,
  Gem as GemIcon,
  ImageOff,
  RefreshCw,
  TriangleAlert,
} from 'lucide-vue-next'
import EmptyState from './EmptyState.vue'
import PageHeader from './PageHeader.vue'
import {
  api,
  ApiError,
  type GemBuy,
  type GemPage,
  type GemQuery,
  type GemScan,
} from '../api'
import { formatNumber, formatRelative, t } from '../i18n'
import { toastError } from '../toast'

// The admin's gem finder: every car for sale, rated by the Go side. This
// page only sends the filters and renders what comes back.

const scan = ref<GemScan | null>(null)
const page = ref<GemPage | null>(null)
const error = ref('')
const loading = ref(true)
const broken = ref(new Set<string>())
const open = ref(new Set<string>())

const query = ref<GemQuery>({
  make: '',
  model: '',
  buy: '',
  sort: '',
  page: 1,
})

const running = computed(
  () => scan.value?.status === 'requested' || scan.value?.status === 'running',
)
const makes = computed(() => [
  ...new Set((page.value?.models ?? []).map((m) => m.make)),
])
const models = computed(() =>
  (page.value?.models ?? [])
    .filter((m) => m.make === query.value.make)
    .map((m) => m.model),
)
const pages = computed(() =>
  page.value
    ? Math.max(1, Math.ceil(page.value.total / page.value.pageSize))
    : 1,
)

const status = computed(() => {
  const s = scan.value
  if (!s) return t('gems.neverScanned')
  if (s.status === 'requested') return t('gems.requested')
  if (s.status === 'running')
    return t('gems.running', { pages: s.pages, ads: s.ads })
  if (s.status === 'failed') return t('gems.failed', { error: s.error ?? '' })
  return t('gems.done', {
    when: formatRelative(s.finishedAt ?? s.requestedAt),
    ads: s.ads,
    rated: s.rated,
    gone: s.gone,
  })
})

const badge: Record<GemBuy, string> = {
  good: 'bg-success-50 text-success-700',
  average: 'bg-gray-100 text-gray-700',
  bad: 'bg-danger-50 text-danger-700',
  check: 'bg-warning-50 text-warning-700',
}

async function load() {
  loading.value = true
  try {
    page.value = await api.gems(query.value)
    error.value = ''
  } catch (reason) {
    error.value =
      reason instanceof ApiError ? reason.message : t('gems.loadFailed')
  } finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
async function refreshScan() {
  const wasRunning = running.value
  try {
    scan.value = await api.gemScan()
  } catch {
    // A missed poll is retried on the next tick.
  }
  if (wasRunning && !running.value) load()
  clearTimeout(timer)
  if (running.value) timer = setTimeout(refreshScan, 3000)
}

async function startScan() {
  try {
    scan.value = await api.requestGemScan()
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

function toggle(id: string) {
  if (open.value.has(id)) open.value.delete(id)
  else open.value.add(id)
}

// Any filter change starts again at page 1; the model belongs to the make.
watch(
  () => query.value.make,
  () => (query.value.model = ''),
)
watch(
  () => [
    query.value.make,
    query.value.model,
    query.value.buy,
    query.value.sort,
    query.value.maxPrice,
    query.value.minYear,
    query.value.maxMileage,
  ],
  () => {
    query.value.page = 1
    load()
  },
)

function goTo(n: number) {
  query.value.page = n
  load()
}

onMounted(() => {
  load()
  refreshScan()
})
onUnmounted(() => clearTimeout(timer))
</script>

<template>
  <section>
    <PageHeader :title="t('gems.title')" :description="t('gems.lead')">
      <button class="btn-primary" :disabled="running" @click="startScan">
        <RefreshCw :size="16" :class="{ 'animate-spin': running }" />
        {{ running ? t('gems.scanning') : t('gems.scan') }}
      </button>
    </PageHeader>

    <p class="mb-4 text-sm text-gray-600" role="status">{{ status }}</p>
    <div
      v-if="scan?.status === 'done' && scan.note"
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

    <div class="mb-4 flex flex-wrap items-center gap-2">
      <select
        v-model="query.make"
        class="field w-auto"
        :aria-label="t('gems.make')"
      >
        <option value="">{{ t('gems.anyMake') }}</option>
        <option v-for="m in makes" :key="m" :value="m">{{ m }}</option>
      </select>
      <select
        v-model="query.model"
        class="field w-auto"
        :aria-label="t('gems.model')"
        :disabled="!query.make"
      >
        <option value="">{{ t('gems.anyModel') }}</option>
        <option v-for="m in models" :key="m" :value="m">{{ m }}</option>
      </select>
      <select
        v-model="query.buy"
        class="field w-auto"
        :aria-label="t('gems.verdict')"
      >
        <option value="">{{ t('gems.anyVerdict') }}</option>
        <option
          v-for="b in ['good', 'average', 'bad', 'check'] as GemBuy[]"
          :key="b"
          :value="b"
        >
          {{ t(`gems.buy.${b}`) }}
        </option>
      </select>
      <input
        v-model.lazy.number="query.maxPrice"
        type="number"
        min="0"
        class="field w-32"
        :placeholder="t('gems.maxPrice')"
        :aria-label="t('gems.maxPrice')"
      />
      <input
        v-model.lazy.number="query.minYear"
        type="number"
        min="1950"
        class="field w-28"
        :placeholder="t('gems.minYear')"
        :aria-label="t('gems.minYear')"
      />
      <input
        v-model.lazy.number="query.maxMileage"
        type="number"
        min="0"
        class="field w-32"
        :placeholder="t('gems.maxMileage')"
        :aria-label="t('gems.maxMileage')"
      />
      <select
        v-model="query.sort"
        class="field w-auto"
        :aria-label="t('gems.sort')"
      >
        <option value="">{{ t('gems.sortBest') }}</option>
        <option value="price">{{ t('gems.sortPrice') }}</option>
        <option value="year">{{ t('gems.sortYear') }}</option>
      </select>
      <span v-if="page" class="ml-auto text-small text-gray-500 tabular-nums">
        {{ t('gems.count', { n: page.total }) }}
      </span>
    </div>

    <div class="card overflow-clip">
      <div
        v-if="loading && !page"
        class="divide-y divide-gray-100"
        role="status"
      >
        <div v-for="n in 4" :key="n" class="flex items-center gap-4 px-4 py-3">
          <div class="skeleton h-12 w-16 rounded-md" />
          <div class="flex-1">
            <div class="skeleton h-4 w-72 max-w-full" />
            <div class="skeleton mt-2 h-3 w-40" />
          </div>
        </div>
      </div>

      <EmptyState
        v-else-if="!page?.items.length"
        :icon="GemIcon"
        :title="t('gems.emptyTitle')"
        :description="t('gems.emptyBody')"
      />

      <ul v-else class="divide-y divide-gray-100" :aria-busy="loading">
        <li v-for="g in page.items" :key="g.externalId" class="px-4 py-3">
          <div class="flex items-center gap-4">
            <div
              class="grid h-12 w-16 shrink-0 place-items-center overflow-hidden rounded-md bg-gray-100 text-gray-400"
            >
              <img
                v-if="g.imageUrl && !broken.has(g.externalId)"
                :src="g.imageUrl"
                alt=""
                class="size-full object-cover"
                loading="lazy"
                referrerpolicy="no-referrer"
                @error="broken.add(g.externalId)"
              />
              <ImageOff v-else :size="16" />
            </div>
            <div class="min-w-0 flex-1">
              <a
                :href="g.url"
                target="_blank"
                rel="noopener noreferrer"
                class="group inline-flex max-w-full items-center gap-1 text-sm text-gray-900 hover:text-brand-600"
              >
                <span class="truncate"
                  >{{ g.make }} {{ g.model }} · {{ g.title }}</span
                >
                <ArrowUpRight
                  :size="14"
                  class="shrink-0 text-gray-400 group-hover:text-brand-600"
                />
              </a>
              <p class="mt-0.5 truncate text-small text-gray-500 tabular-nums">
                {{
                  [
                    g.year,
                    g.mileageKm
                      ? t('gems.km', { km: formatNumber(g.mileageKm) })
                      : '',
                    g.fuel,
                  ]
                    .filter(Boolean)
                    .join(' · ')
                }}
              </p>
            </div>
            <span
              class="shrink-0 rounded-full px-2 py-0.5 text-small font-medium"
              :class="badge[g.buy]"
              >{{ t(`gems.buy.${g.buy}`) }}</span
            >
            <span
              class="w-24 shrink-0 text-right text-sm font-semibold text-gray-900 tabular-nums"
              >{{ g.price }}</span
            >
          </div>
          <p class="mt-2 text-small text-gray-700">{{ g.overall }}</p>
          <button
            class="mt-1 text-small text-brand-600 hover:underline"
            :aria-expanded="open.has(g.externalId)"
            @click="toggle(g.externalId)"
          >
            {{ t('gems.details') }}
          </button>
          <dl v-if="open.has(g.externalId)" class="mt-2 space-y-1 text-small">
            <div v-for="f in g.fields.slice(1)" :key="f.name">
              <dt class="font-medium text-gray-900">{{ f.name }}</dt>
              <dd class="text-gray-700">{{ f.value }}</dd>
            </div>
          </dl>
        </li>
      </ul>
    </div>

    <nav
      v-if="page && pages > 1"
      class="mt-4 flex items-center justify-between text-small text-gray-600"
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
  </section>
</template>
