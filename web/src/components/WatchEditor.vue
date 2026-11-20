<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  watch as watchRef,
} from 'vue'
import {
  ChevronRight,
  CircleAlert,
  ExternalLink,
  FileQuestion,
  Globe,
  MousePointerClick,
  RefreshCw,
  RotateCcw,
  Trash2,
  TriangleAlert,
} from 'lucide-vue-next'
import BaseDialog from './BaseDialog.vue'
import EmptyState from './EmptyState.vue'
import PageHeader from './PageHeader.vue'
import PriceChart from './PriceChart.vue'
import PricePicker from './PricePicker.vue'
import {
  api,
  ApiError,
  type Channel,
  type Extractor,
  type ExtractorTest,
  type InspectedPage,
  type Me,
  type PricePoint,
  type Watch,
  type WatchAlert,
  type WatchInput,
  type WatchRule,
} from '../api'
import {
  formatDate,
  formatMoney,
  formatNumber,
  formatRelative,
  t,
  tKey,
} from '../i18n'
import { navigate, onLinkClick, setLeaveGuard } from '../router'
import { toast, toastError } from '../toast'
import {
  changePercent,
  formatChange,
  hostOf,
  intervalText,
  ruleText,
} from '../watches'

// Creates a price watch (/watches/new): load a page, point at its price,
// choose when to hear about it. On an existing watch (/watches/12) it also
// shows the price, its history and the alerts sent.
const props = defineProps<{ me: Me; id: number | 'new' }>()

const state = ref<'loading' | 'ready' | 'missing' | 'failed'>('loading')
const error = ref('')
const channels = ref<Channel[]>([])
const watch = ref<Watch>()
const history = ref<PricePoint[]>([])
const alerts = ref<WatchAlert[]>([])

// The form.
const name = ref('')
const channelId = ref(0)
const intervalMinutes = ref(60)
const ruleKind = ref<'below' | 'drop'>('drop')
// Bound to <input type="number">, so Vue stores a number once typed.
const targetPrice = ref<string | number>('')
const dropPercent = ref<string | number>('5')
const toNumber = (v: string | number) => Number(String(v).replace(',', '.'))

// The page and the picked price.
const url = ref('')
const page = ref<InspectedPage | null>(null)
const loadingPage = ref(false)
const pageError = ref('')
const extractor = ref<Extractor>({})
const reading = ref<ExtractorTest | null>(null)
const picking = ref(false) // re-picking on an existing watch
const urlInput = ref<HTMLInputElement>()

const saving = ref(false)
const checking = ref(false)
const deleting = ref(false)
const pendingLeave = ref<(() => void) | null>(null)
const showAllHistory = ref(false)

const isNew = computed(() => props.id === 'new')
const intervals = computed(() =>
  props.me.watchIntervals.length ? props.me.watchIntervals : [60],
)
const pickerOpen = computed(() => isNew.value || picking.value)

// The currency and starting price the rule is about.
const currency = computed(
  () =>
    reading.value?.price?.currency ||
    watch.value?.currency ||
    page.value?.currency ||
    '',
)
const referenceCents = computed(() =>
  pickerOpen.value
    ? (reading.value?.price?.cents ?? null)
    : (watch.value?.referenceCents ?? null),
)

const rule = computed<WatchRule>(() =>
  ruleKind.value === 'below'
    ? {
        kind: 'below',
        targetCents: Math.round(toNumber(targetPrice.value) * 100),
      }
    : {
        kind: 'drop',
        dropPercent: toNumber(dropPercent.value),
      },
)
const ruleValid = computed(() =>
  rule.value.kind === 'below'
    ? rule.value.targetCents > 0
    : rule.value.dropPercent > 0 && rule.value.dropPercent < 100,
)
const thresholdCents = computed(() => {
  const r = rule.value
  if (r.kind === 'below') return r.targetCents
  if (referenceCents.value === null) return null
  const basis = Math.round(r.dropPercent * 100)
  return Math.floor((referenceCents.value * (10000 - basis)) / 10000)
})
const lastPrice = computed(() =>
  pickerOpen.value
    ? (reading.value?.price?.cents ?? null)
    : (watch.value?.priceCents ?? null),
)
const alreadyBelow = computed(
  () =>
    ruleValid.value &&
    lastPrice.value !== null &&
    thresholdCents.value !== null &&
    lastPrice.value <= thresholdCents.value,
)

const input = (): WatchInput => ({
  name: name.value.trim(),
  url: pickerOpen.value
    ? (page.value?.url ?? url.value)
    : (watch.value?.url ?? ''),
  snapshot: pickerOpen.value ? page.value?.snapshot : undefined,
  extractor: pickerOpen.value
    ? extractor.value
    : (watch.value?.extractor ?? {}),
  rule: rule.value,
  intervalMinutes: intervalMinutes.value,
  channelId: channelId.value,
})
// What was last loaded or saved; the form is "dirty" when it differs. The
// snapshot id is left out: reloading the same page changes nothing.
const fingerprint = () => JSON.stringify({ ...input(), snapshot: undefined })
const saved = ref('')
const dirty = computed(
  () =>
    state.value === 'ready' &&
    (fingerprint() !== saved.value || (isNew.value && !!page.value)),
)
const canSave = computed(
  () =>
    !saving.value &&
    channels.value.length > 0 &&
    ruleValid.value &&
    !!name.value.trim() &&
    (!pickerOpen.value || !!reading.value?.price?.currency) &&
    (isNew.value || dirty.value),
)

function fill(w: Watch | undefined) {
  name.value = w?.name ?? ''
  channelId.value = w?.channel.id ?? channels.value[0]?.id ?? 0
  intervalMinutes.value =
    w?.intervalMinutes ??
    (intervals.value.includes(360) ? 360 : intervals.value[0]!)
  if (w?.rule.kind === 'below') {
    ruleKind.value = 'below'
    targetPrice.value = String(w.rule.targetCents / 100)
  } else if (w?.rule.kind === 'drop') {
    ruleKind.value = 'drop'
    dropPercent.value = String(w.rule.dropPercent)
  }
  url.value = w?.url ?? ''
}

async function load() {
  state.value = 'loading'
  error.value = ''
  try {
    if (!channels.value.length) channels.value = await api.channels()
    if (isNew.value) {
      watch.value = undefined
      fill(undefined)
    } else {
      const detail = await api.watch(props.id as number)
      watch.value = detail.watch
      history.value = detail.history
      alerts.value = detail.alerts
      fill(detail.watch)
    }
    picking.value = false
    page.value = null
    reading.value = null
    saved.value = fingerprint()
    state.value = 'ready'
    if (isNew.value) {
      await nextTick()
      urlInput.value?.focus()
    }
  } catch (reason) {
    if (reason instanceof ApiError && reason.status === 404) {
      state.value = 'missing'
      return
    }
    error.value =
      reason instanceof ApiError ? reason.message : t('watchEditor.loadFailed')
    state.value = 'failed'
  }
}

async function refresh() {
  if (!watch.value) return
  const detail = await api.watch(watch.value.id)
  watch.value = detail.watch
  history.value = detail.history
  alerts.value = detail.alerts
}

// After an action that worked: reload what the page shows. A failure here
// keeps the page as it was; the action itself still succeeded.
async function refreshAfter() {
  try {
    await refresh()
    return true
  } catch {
    return false
  }
}

async function loadPage() {
  pageError.value = ''
  loadingPage.value = true
  try {
    const p = await api.inspectPage(url.value)
    page.value = p
    url.value = p.url
    reading.value = null
    // Keep the element across reloads. A fresh page starts from its
    // structured data if it has some; never from a hidden form field,
    // which may well be the price before tax.
    const preferred = p.suggestions.find((s) =>
      ['structured', 'meta', 'json'].includes(s.kind),
    )
    if (!extractor.value.selector && !extractor.value.jsonPath && preferred)
      extractor.value = { ...preferred.extractor }
    if (!name.value.trim() && p.title) name.value = p.title.slice(0, 80)
  } catch (reason) {
    pageError.value =
      reason instanceof ApiError ? reason.message : t('watchEditor.pageFailed')
  } finally {
    loadingPage.value = false
  }
}

function startPicking() {
  if (!watch.value) return
  picking.value = true
  url.value = watch.value.url
  extractor.value = { ...watch.value.extractor }
  void loadPage()
}

function cancelPicking() {
  picking.value = false
  page.value = null
  reading.value = null
}

async function save() {
  error.value = ''
  saving.value = true
  try {
    if (watch.value) {
      await api.updateWatch(watch.value.id, input())
      toast(t('toast.watchSaved', { name: input().name }))
      picking.value = false
      page.value = null
      reading.value = null
      if (await refreshAfter()) fill(watch.value)
      saved.value = fingerprint()
    } else {
      const created = await api.createWatch(input())
      toast(t('toast.watchCreated', { name: created.name }))
      saved.value = fingerprint()
      page.value = null
      navigate(`/watches/${created.id}`, { replace: true })
    }
  } catch (reason) {
    error.value =
      reason instanceof ApiError ? reason.message : t('watchEditor.saveFailed')
  } finally {
    saving.value = false
  }
}

async function checkNow() {
  if (!watch.value) return
  checking.value = true
  try {
    const w = await api.checkWatch(watch.value.id)
    await refreshAfter()
    const price =
      w.priceCents !== null ? formatMoney(w.priceCents, w.currency) : ''
    if (w.lastError) toastError(w.lastError)
    else if (w.skipped && price)
      toast(t('toast.watchCheckedRecently', { price }), 'info')
    else if (price) toast(t('toast.watchChecked', { price }))
    else toast(t('toast.watchCheckedNoPrice'))
  } catch (reason) {
    toastError(
      reason instanceof ApiError
        ? reason.message
        : t('watchEditor.checkFailed'),
    )
  } finally {
    checking.value = false
  }
}

async function resetReference() {
  if (!watch.value) return
  try {
    const w = await api.resetWatch(watch.value.id)
    await refreshAfter()
    toast(
      t('toast.watchReset', {
        price: formatMoney(w.referenceCents, w.currency),
      }),
    )
  } catch (reason) {
    toastError(
      reason instanceof ApiError ? reason.message : t('watchEditor.saveFailed'),
    )
  }
}

async function remove() {
  const w = watch.value
  deleting.value = false
  if (!w) return
  try {
    await api.deleteWatch(w.id)
    saved.value = fingerprint()
    page.value = null
    toast(t('toast.watchDeleted', { name: w.name }))
    navigate('/watches', { replace: true })
  } catch (reason) {
    error.value =
      reason instanceof ApiError
        ? reason.message
        : t('watchEditor.deleteFailed')
  }
}

function discard() {
  const proceed = pendingLeave.value
  pendingLeave.value = null
  saved.value = fingerprint()
  page.value = null
  proceed?.()
}

function onBeforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value) event.preventDefault()
}

watchRef(
  () => props.id,
  () => void load(),
)
onMounted(() => {
  void load()
  setLeaveGuard((proceed) => {
    if (!dirty.value) return true
    pendingLeave.value = proceed
    return false
  })
  window.addEventListener('beforeunload', onBeforeUnload)
})
onBeforeUnmount(() => {
  setLeaveGuard(null)
  window.removeEventListener('beforeunload', onBeforeUnload)
})

const shownHistory = computed(() =>
  showAllHistory.value ? history.value : history.value.slice(0, 10),
)
const dateTime = (iso: string) =>
  formatDate(iso, {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
const money = (cents: number | null, cur = currency.value) =>
  cents === null ? '—' : formatMoney(cents, cur)
const describeExtractor = (x: Extractor) =>
  [x.selector, x.attr && `@${x.attr}`, x.jsonPath && `→ ${x.jsonPath}`]
    .filter(Boolean)
    .join(' ')
</script>

<template>
  <section>
    <nav
      :aria-label="t('watchEditor.breadcrumb')"
      class="mb-3 flex min-w-0 items-center gap-1 text-small"
    >
      <a
        href="/watches"
        class="-ml-1.5 shrink-0 rounded-md px-1.5 py-0.5 text-gray-600 hover:bg-gray-150/70 hover:text-gray-900"
        @click="onLinkClick($event, '/watches')"
        >{{ t('watches.title') }}</a
      >
      <ChevronRight :size="14" class="shrink-0 text-gray-400" />
      <span class="truncate px-1.5 font-medium text-gray-900">{{
        watch ? watch.name : t('watches.new')
      }}</span>
    </nav>

    <div
      v-if="state === 'loading'"
      role="status"
      :aria-label="t('common.loading')"
    >
      <div class="skeleton mb-2 h-8 w-64" />
      <div class="skeleton mb-10 h-4 w-96 max-w-full" />
      <div class="card h-64" />
    </div>

    <div v-else-if="state === 'missing'" class="card">
      <EmptyState
        :icon="FileQuestion"
        :title="t('watchEditor.missingTitle')"
        :description="t('watchEditor.missingBody')"
      >
        <button class="btn-secondary" @click="navigate('/watches')">
          {{ t('watchEditor.back') }}
        </button>
      </EmptyState>
    </div>

    <div v-else-if="state === 'failed'" class="card">
      <EmptyState
        :icon="CircleAlert"
        :title="t('watchEditor.loadFailedTitle')"
        :description="error"
      >
        <button class="btn-secondary" @click="load">
          {{ t('common.tryAgain') }}
        </button>
      </EmptyState>
    </div>

    <form v-else @submit.prevent="save">
      <PageHeader :title="watch ? watch.name : t('watches.new')">
        <template #description>
          <template v-if="watch">
            <a
              :href="watch.url"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center gap-1 hover:text-brand-700 hover:underline"
              >{{ hostOf(watch.url) }}<ExternalLink :size="12" /></a
            ><span class="text-gray-400"> · </span
            >{{
              [
                watch.userName !== me.name
                  ? t('watchEditor.createdBy', { name: watch.userName })
                  : '',
                t('watchEditor.created', {
                  date: formatDate(watch.createdAt, {
                    day: 'numeric',
                    month: 'long',
                    year: 'numeric',
                  }),
                }),
                t('watchEditor.alertsSoFar', { n: watch.alerts }),
              ]
                .filter(Boolean)
                .join(' · ')
            }}
            <span v-if="!watch.enabled" class="badge-warning ml-1.5">{{
              t('common.paused')
            }}</span>
          </template>
          <template v-else>{{ t('watchEditor.newLead') }}</template>
        </template>
        <button
          v-if="watch"
          type="button"
          class="btn-secondary text-danger-600 hover:text-danger-700"
          @click="deleting = true"
        >
          <Trash2 :size="15" />{{ t('common.delete') }}
        </button>
      </PageHeader>

      <!-- An existing watch: the price now and over time -->
      <section
        v-if="watch"
        class="mb-8 grid gap-4 lg:grid-cols-[280px_minmax(0,1fr)]"
        aria-labelledby="price-heading"
      >
        <div class="card flex flex-col p-5">
          <h2 id="price-heading" class="text-small font-medium text-gray-500">
            {{ t('watchEditor.current') }}
          </h2>
          <p class="mt-1 text-display font-semibold text-gray-900 tabular-nums">
            {{ money(watch.priceCents, watch.currency) }}
          </p>
          <p
            v-if="changePercent(watch) !== null"
            class="text-small tabular-nums"
            :class="
              changePercent(watch)! < 0 ? 'text-success-700' : 'text-gray-600'
            "
          >
            {{ formatChange(changePercent(watch)!) }}
            <span class="text-gray-500">{{
              t('watchEditor.vsReference', {
                price: money(watch.referenceCents, watch.currency),
              })
            }}</span>
          </p>
          <dl class="mt-4 grid gap-2 text-small">
            <div class="flex justify-between gap-3">
              <dt class="text-gray-500">{{ t('watchEditor.alertWhen') }}</dt>
              <dd class="text-right text-gray-900">
                {{ ruleText(watch.rule, watch.thresholdCents, watch.currency) }}
              </dd>
            </div>
            <div class="flex justify-between gap-3">
              <dt class="text-gray-500">{{ t('watchEditor.lastCheck') }}</dt>
              <dd class="text-right text-gray-900">
                {{
                  watch.checkedAt
                    ? formatRelative(watch.checkedAt)
                    : t('watches.never')
                }}
              </dd>
            </div>
            <div v-if="watch.enabled" class="flex justify-between gap-3">
              <dt class="text-gray-500">{{ t('watchEditor.nextCheck') }}</dt>
              <dd class="text-right text-gray-900">
                {{ formatRelative(watch.nextCheckAt) }}
              </dd>
            </div>
          </dl>
          <span v-if="watch.below" class="badge-success mt-3 self-start">{{
            t('watches.belowNow')
          }}</span>
          <div class="mt-auto flex flex-wrap gap-2 pt-5">
            <button
              type="button"
              class="btn-secondary"
              :disabled="checking"
              @click="checkNow"
            >
              <RefreshCw :size="14" :class="{ 'animate-spin': checking }" />{{
                checking ? t('watchEditor.checking') : t('watchEditor.checkNow')
              }}
            </button>
            <button
              v-if="
                watch.rule.kind === 'drop' &&
                watch.priceCents !== null &&
                watch.priceCents !== watch.referenceCents
              "
              type="button"
              class="btn-subtle"
              :title="t('watchEditor.resetHint')"
              @click="resetReference"
            >
              <RotateCcw :size="14" />{{ t('watchEditor.reset') }}
            </button>
          </div>
        </div>

        <div class="card min-w-0 p-5">
          <div
            v-if="watch.failing || watch.failures"
            role="status"
            class="message-bar-warning mb-4"
          >
            <TriangleAlert
              :size="16"
              class="mt-0.5 shrink-0 text-warning-600"
            />
            <div class="min-w-0">
              <p class="font-medium">
                {{
                  watch.failing
                    ? t('watchEditor.failingTitle')
                    : t('watchEditor.failedOnce')
                }}
              </p>
              <p class="mt-0.5 text-small break-words text-gray-700">
                {{ watch.lastError }}
              </p>
              <p v-if="watch.failing" class="mt-1 text-small text-gray-700">
                {{ t('watchEditor.failingBody', { n: watch.failures }) }}
              </p>
            </div>
          </div>
          <h2 class="mb-2 text-small font-medium text-gray-500">
            {{ t('watchEditor.chartTitle') }}
          </h2>
          <PriceChart
            v-if="history.length"
            :history="history"
            :currency="watch.currency"
            :threshold-cents="watch.thresholdCents"
          />
        </div>
      </section>

      <!-- The page -->
      <section
        class="grid gap-x-12 gap-y-4 border-t border-gray-200 py-8 lg:grid-cols-[minmax(0,280px)_minmax(0,1fr)]"
        aria-labelledby="page-heading"
      >
        <div>
          <h2 id="page-heading" class="text-base font-semibold">
            {{ t('watchEditor.pageTitle') }}
          </h2>
          <p class="mt-1 text-small text-gray-500">
            {{ t('watchEditor.pageLead') }}
          </p>
        </div>
        <div class="card p-5 sm:p-6">
          <template v-if="pickerOpen">
            <label class="field-label" for="watch-url">{{
              t('watchEditor.url')
            }}</label>
            <div class="flex gap-2">
              <div
                class="field-within flex min-w-0 flex-1 items-center gap-2 px-2.5"
              >
                <Globe :size="15" class="shrink-0 text-gray-400" />
                <input
                  id="watch-url"
                  ref="urlInput"
                  v-model="url"
                  type="url"
                  inputmode="url"
                  autocomplete="off"
                  spellcheck="false"
                  class="h-8 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-gray-400"
                  :placeholder="t('watchEditor.urlPlaceholder')"
                  @keydown.enter.prevent="loadPage"
                />
              </div>
              <button
                type="button"
                class="btn-primary"
                :disabled="loadingPage || !url.trim()"
                @click="loadPage"
              >
                <RefreshCw v-if="loadingPage" :size="14" class="animate-spin" />
                {{
                  loadingPage
                    ? t('watchEditor.loading')
                    : page
                      ? t('watchEditor.reload')
                      : t('watchEditor.load')
                }}
              </button>
              <button
                v-if="picking"
                type="button"
                class="btn-secondary"
                @click="cancelPicking"
              >
                {{ t('common.cancel') }}
              </button>
            </div>
            <p v-if="!page && !pageError" class="field-hint">
              {{ t('watchEditor.urlHint') }}
            </p>
            <div v-if="pageError" role="alert" class="message-bar-danger mt-3">
              <CircleAlert
                :size="16"
                class="mt-0.5 shrink-0 text-danger-600"
              />{{ pageError }}
            </div>
          </template>
          <template v-else-if="watch">
            <dl class="grid gap-3 text-small">
              <div>
                <dt class="text-gray-500">{{ t('watchEditor.url') }}</dt>
                <dd class="mt-0.5 break-all text-gray-900">{{ watch.url }}</dd>
              </div>
              <div>
                <dt class="text-gray-500">{{ t('watchEditor.where') }}</dt>
                <dd class="mt-0.5 font-mono text-xs break-all text-gray-800">
                  {{ describeExtractor(watch.extractor) }}
                </dd>
              </div>
              <div v-if="watch.raw">
                <dt class="text-gray-500">{{ t('watchEditor.lastText') }}</dt>
                <dd class="mt-0.5 font-mono text-xs text-gray-800">
                  “{{ watch.raw }}”
                </dd>
              </div>
            </dl>
            <button
              type="button"
              class="btn-secondary mt-4"
              @click="startPicking"
            >
              <MousePointerClick :size="15" />{{ t('watchEditor.pickAgain') }}
            </button>
          </template>
        </div>
      </section>

      <section
        v-if="pickerOpen && page"
        class="pb-8"
        :aria-label="t('picker.region')"
      >
        <PricePicker
          v-model="extractor"
          :page="page"
          @reading="reading = $event"
        />
        <p
          v-if="picking && reading?.price"
          class="mt-3 text-small text-gray-600"
        >
          {{
            t('watchEditor.restartNote', {
              price: money(reading.price.cents, reading.price.currency),
            })
          }}
        </p>
      </section>

      <!-- When to alert -->
      <section
        class="grid gap-x-12 gap-y-4 border-t border-gray-200 py-8 lg:grid-cols-[minmax(0,280px)_minmax(0,1fr)]"
        aria-labelledby="rule-heading"
      >
        <div>
          <h2 id="rule-heading" class="text-base font-semibold">
            {{ t('watchEditor.ruleTitle') }}
          </h2>
          <p class="mt-1 text-small text-gray-500">
            {{ t('watchEditor.ruleLead') }}
          </p>
        </div>
        <div class="card grid gap-3 p-5 sm:p-6">
          <label
            v-for="kind in ['drop', 'below'] as const"
            :key="kind"
            class="flex cursor-pointer gap-3 rounded-md p-3 ring-1 transition-colors"
            :class="
              ruleKind === kind
                ? 'bg-brand-50/60 ring-brand-200'
                : 'ring-gray-200 hover:bg-gray-25'
            "
          >
            <input
              v-model="ruleKind"
              type="radio"
              name="rule"
              :value="kind"
              class="mt-1 accent-brand-600"
            />
            <span class="min-w-0 flex-1">
              <span class="block text-sm font-medium text-gray-900">{{
                kind === 'drop'
                  ? t('watchEditor.ruleDrop')
                  : t('watchEditor.ruleBelow')
              }}</span>
              <span class="mt-2 flex items-center gap-2">
                <template v-if="kind === 'drop'">
                  <input
                    v-model="dropPercent"
                    type="number"
                    min="0.5"
                    max="99"
                    step="0.5"
                    class="field w-24 tabular-nums"
                    :aria-label="t('watchEditor.dropPercent')"
                    :disabled="ruleKind !== 'drop'"
                  /><span class="text-gray-600">%</span>
                </template>
                <template v-else>
                  <input
                    v-model="targetPrice"
                    type="number"
                    min="0"
                    step="any"
                    class="field w-36 tabular-nums"
                    :aria-label="t('watchEditor.targetPrice')"
                    :placeholder="
                      lastPrice !== null
                        ? formatNumber(Math.floor((lastPrice * 0.9) / 100))
                        : ''
                    "
                    :disabled="ruleKind !== 'below'"
                  /><span class="text-gray-600">{{ currency }}</span>
                </template>
              </span>
              <span
                v-if="
                  ruleKind === kind &&
                  kind === 'drop' &&
                  referenceCents !== null &&
                  thresholdCents !== null &&
                  ruleValid
                "
                class="field-hint block"
                >{{
                  t('watchEditor.dropHint', {
                    price: money(referenceCents),
                    threshold: money(thresholdCents),
                  })
                }}</span
              >
              <span
                v-if="
                  ruleKind === kind && kind === 'below' && lastPrice !== null
                "
                class="field-hint block"
                >{{
                  t('watchEditor.belowHint', { price: money(lastPrice) })
                }}</span
              >
            </span>
          </label>
          <p
            v-if="alreadyBelow && (isNew || picking)"
            class="flex items-start gap-1.5 text-xs text-warning-700"
          >
            <TriangleAlert :size="13" class="mt-px shrink-0" />{{
              t('watchEditor.alreadyBelow')
            }}
          </p>
        </div>
      </section>

      <!-- Name, schedule, destination -->
      <section
        class="grid gap-x-12 gap-y-4 border-t border-gray-200 py-8 lg:grid-cols-[minmax(0,280px)_minmax(0,1fr)]"
        aria-labelledby="details-heading"
      >
        <div>
          <h2 id="details-heading" class="text-base font-semibold">
            {{ t('watchEditor.detailsTitle') }}
          </h2>
          <p class="mt-1 text-small text-gray-500">
            {{ t('watchEditor.detailsLead') }}
          </p>
        </div>
        <div class="card grid gap-5 p-5 sm:grid-cols-2 sm:p-6">
          <label class="block sm:col-span-2">
            <span class="field-label">{{ t('watchEditor.name') }}</span>
            <input
              v-model="name"
              class="field sm:max-w-md"
              maxlength="80"
              required
              :placeholder="t('watchEditor.namePlaceholder')"
            />
          </label>
          <label class="block">
            <span class="field-label">{{ t('watchEditor.interval') }}</span>
            <select v-model.number="intervalMinutes" class="field">
              <option v-for="m in intervals" :key="m" :value="m">
                {{ intervalText(m) }}
              </option>
            </select>
          </label>
          <label class="block">
            <span class="field-label">{{ t('watchEditor.sendTo') }}</span>
            <select
              v-model="channelId"
              class="field"
              :disabled="!channels.length"
            >
              <option v-for="ch in channels" :key="ch.id" :value="ch.id">
                {{ ch.label || `${ch.kind} ${ch.target}` }}
              </option>
            </select>
          </label>
          <div
            v-if="!channels.length"
            class="message-bar-warning sm:col-span-2"
          >
            <TriangleAlert
              :size="16"
              class="mt-0.5 shrink-0 text-warning-600"
            />
            <span
              >{{ t('editor.noChannelBefore') }}
              <span class="kbd">/dashboard</span>
              {{ t('editor.noChannelAfter') }}</span
            >
          </div>
        </div>
      </section>

      <!-- History -->
      <section
        v-if="watch"
        class="grid gap-x-12 gap-y-4 border-t border-gray-200 py-8 lg:grid-cols-[minmax(0,280px)_minmax(0,1fr)]"
        aria-labelledby="history-heading"
      >
        <div>
          <h2 id="history-heading" class="text-base font-semibold">
            {{ t('watchEditor.historyTitle') }}
          </h2>
          <p class="mt-1 text-small text-gray-500">
            {{ t('watchEditor.historyLead') }}
          </p>
        </div>
        <div class="grid min-w-0 gap-4">
          <div class="card overflow-hidden">
            <table class="w-full text-sm">
              <thead class="border-b border-gray-150 bg-gray-25">
                <tr>
                  <th class="table-head">{{ t('watchEditor.colWhen') }}</th>
                  <th class="table-head text-right">
                    {{ t('watchEditor.colPrice') }}
                  </th>
                  <th class="table-head hidden sm:table-cell">
                    {{ t('watchEditor.colRead') }}
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100">
                <tr v-for="(p, i) in shownHistory" :key="p.at + i">
                  <td class="px-4 py-2 whitespace-nowrap text-gray-600">
                    {{ dateTime(p.at) }}
                  </td>
                  <td
                    class="px-4 py-2 text-right whitespace-nowrap tabular-nums"
                  >
                    <span v-if="p.ok" class="font-medium text-gray-900">{{
                      money(p.cents, p.currency)
                    }}</span>
                    <span v-else class="text-warning-700">{{
                      t('watchEditor.readFailed')
                    }}</span>
                  </td>
                  <td
                    class="hidden max-w-0 truncate px-4 py-2 font-mono text-xs text-gray-500 sm:table-cell"
                    :title="p.ok ? p.raw : p.error"
                  >
                    {{ p.ok ? p.raw : p.error }}
                  </td>
                </tr>
              </tbody>
            </table>
            <button
              v-if="history.length > 10"
              type="button"
              class="w-full border-t border-gray-150 py-2 text-small font-medium text-brand-700 hover:bg-gray-25"
              @click="showAllHistory = !showAllHistory"
            >
              {{
                showAllHistory
                  ? t('watchEditor.showLess')
                  : t('watchEditor.showAll', { n: history.length })
              }}
            </button>
          </div>

          <div class="card p-5">
            <h3 class="text-small font-semibold text-gray-900">
              {{ t('watchEditor.alertsTitle') }}
            </h3>
            <p v-if="!alerts.length" class="mt-1 text-small text-gray-500">
              {{ t('watchEditor.noAlerts') }}
            </p>
            <ul v-else class="mt-2 grid gap-1.5">
              <li
                v-for="(a, i) in alerts"
                :key="a.at + i"
                class="flex items-center gap-3 text-small"
              >
                <span
                  :class="
                    a.kind === 'drop'
                      ? 'badge-success'
                      : a.kind === 'failing'
                        ? 'badge-warning'
                        : 'badge-neutral'
                  "
                  >{{ tKey(`watchEditor.alertKinds.${a.kind}`, a.kind) }}</span
                >
                <span class="font-medium text-gray-900 tabular-nums">{{
                  a.cents === null ? '' : money(a.cents)
                }}</span>
                <span class="ml-auto text-gray-500">{{ dateTime(a.at) }}</span>
                <span v-if="!a.sent" class="text-xs text-gray-500">{{
                  t('watchEditor.sending')
                }}</span>
              </li>
            </ul>
          </div>
        </div>
      </section>

      <div class="action-bar">
        <div
          v-if="error"
          role="alert"
          class="message-bar-danger mx-4 mt-3 sm:mx-8 lg:mx-10"
        >
          <CircleAlert :size="16" class="mt-0.5 shrink-0 text-danger-600" />{{
            error
          }}
        </div>
        <div class="flex items-center gap-3 px-4 py-3 sm:px-8 lg:px-10">
          <p
            class="hidden min-w-0 flex-1 truncate text-small text-gray-600 md:block"
          >
            <template v-if="ruleValid && thresholdCents !== null && currency">
              {{
                t('watchEditor.summary', {
                  rule: ruleText(rule, thresholdCents, currency),
                  interval: intervalText(intervalMinutes),
                })
              }}
            </template>
            <template v-else-if="pickerOpen && !reading?.price">
              {{ t('watchEditor.pickFirst') }}
            </template>
          </p>
          <span
            v-if="dirty && !isNew"
            class="ml-auto flex items-center gap-1.5 text-small text-gray-500 md:ml-0"
            ><span class="size-1.5 rounded-full bg-warning-600" />{{
              t('editor.unsaved')
            }}</span
          >
          <button
            type="button"
            class="btn-secondary"
            :class="{ 'ml-auto md:ml-0': !dirty || isNew }"
            @click="navigate('/watches')"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            class="btn-primary min-w-28"
            :disabled="!canSave"
          >
            {{
              saving
                ? t('editor.saving')
                : watch
                  ? t('editor.save')
                  : t('watchEditor.create')
            }}
          </button>
        </div>
      </div>
    </form>

    <BaseDialog
      v-if="pendingLeave"
      variant="modal"
      :title="t('editor.discardTitle')"
      @close="pendingLeave = null"
    >
      <p class="text-sm text-gray-600">{{ t('editor.discardBody') }}</p>
      <template #footer>
        <button class="btn-secondary" @click="pendingLeave = null">
          {{ t('editor.keepEditing') }}
        </button>
        <button class="btn-danger" @click="discard">
          {{ t('editor.discard') }}
        </button>
      </template>
    </BaseDialog>

    <BaseDialog
      v-if="deleting && watch"
      variant="modal"
      :title="t('watches.deleteTitle')"
      light-dismiss
      @close="deleting = false"
    >
      <p class="text-sm text-gray-600">
        {{ t('watches.deleteBody', { name: watch.name }) }}
      </p>
      <template #footer>
        <button class="btn-secondary" @click="deleting = false">
          {{ t('common.cancel') }}
        </button>
        <button class="btn-danger" @click="remove">
          {{ t('common.delete') }}
        </button>
      </template>
    </BaseDialog>
  </section>
</template>
