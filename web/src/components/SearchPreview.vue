<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import {
  ArrowUpRight,
  CircleAlert,
  ImageOff,
  Info,
  Radio,
  SearchX,
} from 'lucide-vue-next'
import {
  ApiError,
  previewLive,
  previewSearch,
  type FieldSpec,
  type Filters,
  type Preview,
} from '../api'
import { fieldLabel, formatDate, t, tKey } from '../i18n'

// Which already collected ads the search, as it is being edited, would have
// matched. Re-checked a moment after each change; nothing is saved or sent.
// "Check live" asks the site once for its newest ads instead (a button, not
// on every edit: it is a request to someone else's site).
const props = defineProps<{
  source: string
  category: string
  categoryLabel: string
  filters: Filters
  fields: FieldSpec[]
}>()
const emit = defineEmits<{ result: [Preview | null] }>()

const preview = ref<Preview | null>(null)
const loading = ref(false)
const error = ref('')
const broken = ref(new Set<number>())
// The last live check, until the search changes.
const live = ref<Preview | null>(null)
const liveLoading = ref(false)
const liveError = ref('')
const shown = computed(() => live.value ?? preview.value)

async function checkLive() {
  liveLoading.value = true
  liveError.value = ''
  const key = searchKey()
  try {
    const result = await previewLive({
      source: props.source,
      category: props.category,
      filters: props.filters,
    })
    if (key === searchKey()) live.value = result // not for a search edited meanwhile
  } catch (reason) {
    liveError.value =
      reason instanceof ApiError ? reason.message : t('preview.liveFailed')
  } finally {
    liveLoading.value = false
  }
}
let controller: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined

async function run() {
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  error.value = ''
  try {
    preview.value = await previewSearch(
      {
        source: props.source,
        category: props.category,
        filters: props.filters,
      },
      controller.signal,
    )
  } catch (reason) {
    if (reason instanceof DOMException && reason.name === 'AbortError') return
    preview.value = null
    error.value =
      reason instanceof ApiError ? reason.message : t('preview.failed')
  }
  loading.value = false
  emit('result', preview.value)
}

const searchKey = () =>
  JSON.stringify([props.source, props.category, props.filters])

watch(
  searchKey,
  () => {
    live.value = null
    liveError.value = ''
    clearTimeout(timer)
    timer = setTimeout(run, preview.value ? 400 : 0)
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  clearTimeout(timer)
  controller?.abort()
})

const label = (key: string) =>
  fieldLabel(key, props.fields.find((f) => f.key === key)?.label ?? key)
const unsureLabels = computed(() =>
  (shown.value?.unsureKeys ?? []).map(label).join(', '),
)
const since = computed(() =>
  preview.value?.since
    ? formatDate(preview.value.since, {
        day: 'numeric',
        month: 'long',
      })
    : '',
)
// The category as an adjective: "938 collected car ads".
const kind = computed(() =>
  tKey(
    `preview.kinds.${props.category}`,
    props.categoryLabel.toLowerCase().replace(/s$/, ''),
  ),
)

function seen(at: string) {
  return formatDate(at, {
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  })
}
</script>

<template>
  <div class="card overflow-hidden" :aria-busy="loading">
    <div
      class="flex items-start gap-4 border-b border-gray-150 px-5 py-4 sm:px-6"
    >
      <div class="min-w-0 flex-1">
        <p v-if="live" class="text-sm text-gray-900" aria-live="polite">
          {{
            t('preview.liveLine', {
              n: live.matched,
              matched: live.matched,
              scanned: live.scanned,
              site: source,
            })
          }}<span v-if="live.unsure" class="text-gray-500">
            · {{ t('preview.moreUnchecked', { n: live.unsure }) }}</span
          >
        </p>
        <p
          v-else-if="shown && shown.inScope"
          class="text-sm text-gray-900"
          aria-live="polite"
        >
          {{
            t('preview.matchLine', {
              n: shown.matched,
              matched: shown.matched,
              inScope: shown.inScope,
              kind,
            })
          }}<span v-if="shown.unsure" class="text-gray-500">
            · {{ t('preview.moreUnchecked', { n: shown.unsure }) }}</span
          >
        </p>
        <p v-else-if="shown" class="text-sm text-gray-900">
          {{ t('preview.noneYet', { kind }) }}
        </p>
        <div v-else-if="!error" class="skeleton h-4 w-56" />
        <p v-if="live" class="mt-0.5 text-xs text-gray-500">
          {{ t('preview.liveFrom', { site: source }) }}
        </p>
        <p v-else-if="since && shown" class="mt-0.5 text-xs text-gray-500">
          {{ t('preview.from', { n: shown.scanned, date: since }) }}
        </p>
      </div>
      <span
        v-if="loading || liveLoading"
        class="mt-0.5 size-4 shrink-0 animate-spin rounded-full border-2 border-gray-200 border-t-brand-600"
        role="status"
        :aria-label="t('preview.checking')"
      />
      <button
        v-if="live"
        type="button"
        class="btn-secondary h-8 shrink-0 px-3 text-small"
        @click="live = null"
      >
        {{ t('preview.liveBack') }}
      </button>
      <button
        v-else
        type="button"
        class="btn-secondary h-8 shrink-0 px-3 text-small"
        :disabled="liveLoading || !!error"
        :title="t('preview.liveHint', { site: source })"
        @click="checkLive"
      >
        <Radio :size="14" />{{ t('preview.liveButton', { site: source }) }}
      </button>
    </div>

    <div v-if="liveError" role="alert" class="message-bar-danger m-4">
      <CircleAlert :size="16" class="mt-0.5 shrink-0 text-danger-600" />{{
        liveError
      }}
    </div>

    <div v-if="error && !live" role="alert" class="message-bar-danger m-4">
      <CircleAlert :size="16" class="mt-0.5 shrink-0 text-danger-600" />{{
        error
      }}
    </div>

    <template v-else-if="shown">
      <div
        v-if="shown.unsure"
        class="flex items-start gap-2.5 border-b border-gray-150 bg-gray-25 px-5 py-3 text-small text-gray-700 sm:px-6"
      >
        <Info :size="16" class="mt-0.5 shrink-0 text-gray-500" />
        <span>{{
          t('preview.uncheckedNote', { n: shown.unsure, keys: unsureLabels })
        }}</span>
      </div>

      <div
        v-if="!shown.inScope"
        class="flex flex-col items-center px-6 py-10 text-center"
      >
        <SearchX :size="20" class="mb-3 text-gray-400" />
        <p class="text-sm font-medium text-gray-900">
          {{ t(live ? 'preview.liveNothingTitle' : 'preview.nothingTitle') }}
        </p>
        <p class="mt-1 max-w-sm text-small text-gray-500">
          {{
            live
              ? t('preview.liveNothingBody', { site: source })
              : t('preview.nothingBody')
          }}
        </p>
      </div>
      <div
        v-else-if="!shown.ads.length"
        class="flex flex-col items-center px-6 py-10 text-center"
      >
        <SearchX :size="20" class="mb-3 text-gray-400" />
        <p class="text-sm font-medium text-gray-900">
          {{ t('preview.noMatchTitle') }}
        </p>
        <p class="mt-1 max-w-sm text-small text-gray-500">
          {{
            live
              ? t('preview.liveNoMatchBody', { site: source })
              : t('preview.noMatchBody')
          }}
        </p>
      </div>

      <ul v-else class="max-h-[480px] divide-y divide-gray-100 overflow-y-auto">
        <li v-for="ad in shown.ads" :key="ad.id">
          <a
            :href="ad.url"
            target="_blank"
            rel="noopener noreferrer"
            class="group flex items-center gap-3.5 px-5 py-2.5 transition-colors hover:bg-gray-25 sm:px-6"
          >
            <div
              class="grid h-10 w-14 shrink-0 place-items-center overflow-hidden rounded-md bg-gray-100 text-gray-400"
            >
              <img
                v-if="ad.imageUrl && !broken.has(ad.id)"
                :src="ad.imageUrl"
                alt=""
                class="size-full object-cover"
                loading="lazy"
                referrerpolicy="no-referrer"
                @error="broken.add(ad.id)"
              />
              <ImageOff v-else :size="14" />
            </div>
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm text-gray-900">{{ ad.title }}</p>
              <p class="mt-0.5 truncate text-xs text-gray-500 tabular-nums">
                {{
                  [
                    seen(ad.firstSeen),
                    ad.active ? '' : t('preview.noLongerListed'),
                  ]
                    .filter(Boolean)
                    .join(' · ')
                }}
              </p>
            </div>
            <span v-if="!ad.sure" class="badge-warning hidden sm:inline-flex">{{
              t('preview.unchecked')
            }}</span>
            <span
              v-if="ad.price"
              class="shrink-0 text-sm font-semibold text-gray-900 tabular-nums"
              >{{ ad.price }}</span
            >
            <ArrowUpRight
              :size="15"
              class="shrink-0 text-gray-400 transition-colors group-hover:text-brand-600"
              aria-hidden="true"
            />
          </a>
        </li>
      </ul>
      <p
        v-if="shown.ads.length < shown.matched + shown.unsure"
        class="border-t border-gray-150 px-5 py-2.5 text-xs text-gray-500 sm:px-6"
      >
        {{
          t('preview.showing', {
            shown: shown.ads.length,
            total: shown.matched + shown.unsure,
          })
        }}
      </p>
    </template>
  </div>
</template>
