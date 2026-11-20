<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  CircleAlert,
  CircleCheck,
  Eraser,
  ExternalLink,
  Info,
  MousePointerClick,
  Sparkles,
} from 'lucide-vue-next'
import JsonTree from './JsonTree.vue'
import {
  api,
  ApiError,
  snapshotURL,
  type Extractor,
  type ExtractorTest,
  type InspectedPage,
  type Suggestion,
} from '../api'
import { formatMoney, t, tKey } from '../i18n'

// Point at the price on a page, the way dev tools inspect an element. The
// page is the server's own copy (what the watch will read later), shown in a
// sandboxed iframe whose picker script reports what was clicked. Every
// change is read back through the server, so "The monitor reads" is exactly
// what the watch will see.
const props = defineProps<{ page: InspectedPage; modelValue: Extractor }>()
const emit = defineEmits<{
  'update:modelValue': [Extractor]
  reading: [ExtractorTest | null]
}>()

type Picked = {
  selector: string
  text: string
  tag: string
  attrs: { name: string; value: string }[]
  chain: string[]
  depth: number
}

const frame = ref<HTMLIFrameElement>()
const picked = ref<Picked | null>(null)
const selector = ref(props.modelValue.selector ?? '')
const jsonPath = ref(props.modelValue.jsonPath ?? '')
const attr = ref(props.modelValue.attr ?? '')
const highlight = ref<{
  valid: boolean
  count: number
  visible: boolean
} | null>(null)
const reading = ref<ExtractorTest | null>(null)
const testing = ref(false)
const expired = ref(false)
const showHint = ref(false)
// What a click in the page does: pick the price, or hide what is in the way
// (a cookie banner the script-free copy can't close).
const mode = ref<'pick' | 'remove'>('pick')
const hiddenCount = ref(0)
const autoHidden = ref(0) // of those, hidden on load as cookie banners
const modes = [
  {
    id: 'pick' as const,
    icon: MousePointerClick,
    label: 'picker.modePick' as const,
  },
  { id: 'remove' as const, icon: Eraser, label: 'picker.modeRemove' as const },
]

const isJSON = computed(() => props.page.kind === 'json')

const extractor = computed<Extractor>(() => {
  const x: Extractor = { currency: props.page.currency || undefined }
  if (isJSON.value) {
    if (jsonPath.value.trim()) x.jsonPath = jsonPath.value.trim()
    // A JSON-LD suggestion keeps its script selector even on HTML pages.
    if (props.modelValue.selector && props.modelValue.jsonPath)
      x.selector = props.modelValue.selector
    return x
  }
  if (selector.value.trim()) x.selector = selector.value.trim()
  if (attr.value) x.attr = attr.value
  if (props.modelValue.jsonPath && props.modelValue.selector === x.selector)
    x.jsonPath = props.modelValue.jsonPath
  return x
})

const same = (a: Extractor, b: Extractor) =>
  (a.selector ?? '') === (b.selector ?? '') &&
  (a.attr ?? '') === (b.attr ?? '') &&
  (a.jsonPath ?? '') === (b.jsonPath ?? '')

// --- talking to the picker in the iframe -----------------------------------

function post(message: Record<string, unknown>) {
  frame.value?.contentWindow?.postMessage(message, '*')
}

function onMessage(event: MessageEvent) {
  if (!frame.value || event.source !== frame.value.contentWindow) return
  const data = event.data as { type?: string } & Record<string, unknown>
  if (data.type === 'hidden') {
    hiddenCount.value = Number(data.count) || 0
    autoHidden.value = Number(data.auto) || 0
  } else if (data.type === 'ready') {
    post({ type: 'config', label: t('picker.badge') })
    post({ type: 'mode', mode: mode.value })
    if (selector.value)
      post({
        type: 'highlight',
        selector: selector.value,
        pick: true,
        scroll: true,
      })
  } else if (data.type === 'picked') {
    const p = data as unknown as Picked
    picked.value = p
    selector.value = p.selector
    // Meta tags and form fields hold their value in an attribute.
    const preferred = p.attrs.find((a) => ['content', 'value'].includes(a.name))
    attr.value = !p.text && preferred ? preferred.name : ''
    highlight.value = { valid: true, count: 1, visible: true }
    commit()
  } else if (data.type === 'highlighted') {
    highlight.value = data as unknown as {
      valid: boolean
      count: number
      visible: boolean
    }
  }
}

function selectDepth(depth: number) {
  post({ type: 'depth', depth })
}

function setMode(next: 'pick' | 'remove') {
  mode.value = next
  post({ type: 'mode', mode: next })
}

// Only cookie banners hidden on load: show them all again. Else undo the last.
const onlyAuto = computed(
  () => hiddenCount.value > 0 && autoHidden.value === hiddenCount.value,
)
function unhide() {
  post({ type: 'unhide', all: onlyAuto.value })
}

// --- reading it back through the server ------------------------------------

let pending: AbortController | null = null
let typing: ReturnType<typeof setTimeout> | undefined

async function test() {
  pending?.abort()
  const x = extractor.value
  if (!x.selector && !x.jsonPath) {
    reading.value = null
    emit('reading', null)
    return
  }
  const controller = new AbortController()
  pending = controller
  testing.value = true
  try {
    reading.value = await api.testExtractor(
      props.page.snapshot,
      x,
      controller.signal,
    )
    expired.value = false
  } catch (reason) {
    if (controller.signal.aborted) return
    if (reason instanceof ApiError && reason.status === 410)
      expired.value = true
    reading.value = null
  } finally {
    if (pending === controller) testing.value = false
  }
  emit('reading', reading.value?.price ? reading.value : null)
}

function commit() {
  if (
    !same(extractor.value, props.modelValue) ||
    extractor.value.currency !== props.modelValue.currency
  )
    emit('update:modelValue', extractor.value)
  void test()
}

// A typed selector: show its matches in the page, then read it.
function onSelectorInput() {
  clearTimeout(typing)
  typing = setTimeout(() => {
    picked.value = null
    post({ type: 'highlight', selector: selector.value, scroll: true })
    commit()
  }, 350)
}

function onPathInput() {
  clearTimeout(typing)
  typing = setTimeout(commit, 350)
}

function pickPath(path: string) {
  jsonPath.value = path
  commit()
}

function useSuggestion(s: Suggestion) {
  const x = s.extractor
  selector.value = x.selector ?? ''
  attr.value = x.attr ?? ''
  jsonPath.value = x.jsonPath ?? ''
  picked.value = null
  emit('update:modelValue', {
    ...x,
    currency: x.currency || props.page.currency || undefined,
  })
  if (!isJSON.value && x.selector)
    post({ type: 'highlight', selector: x.selector, pick: true, scroll: true })
  setTimeout(test)
}

const inUse = (s: Suggestion) => same(s.extractor, props.modelValue)

function previewSuggestion(s: Suggestion | null) {
  if (isJSON.value) return
  post({
    type: 'highlight',
    selector: (s ?? null)?.extractor.selector ?? selector.value,
  })
}

// A newly loaded page starts over.
watch(
  () => props.page.snapshot,
  () => {
    picked.value = null
    highlight.value = null
    expired.value = false
    hiddenCount.value = autoHidden.value = 0
    mode.value = 'pick'
    void test()
  },
)

onMounted(() => {
  window.addEventListener('message', onMessage)
  if (props.modelValue.selector || props.modelValue.jsonPath) void test()
})
onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage)
  pending?.abort()
  clearTimeout(typing)
})

const crumbs = computed(() =>
  picked.value
    ? picked.value.chain
        .slice(0, 6)
        .map((label, depth) => ({ label, depth }))
        .reverse()
    : [],
)

const matchText = computed(() => {
  const h = highlight.value
  if (!h || !selector.value.trim()) return ''
  if (!h.valid) return t('picker.invalid')
  if (h.count === 0) return t('picker.noMatch')
  return (
    t('picker.matches', { n: h.count }) +
    (h.visible ? '' : ` · ${t('picker.hidden')}`)
  )
})

const host = computed(() => {
  try {
    return new URL(props.page.url).host
  } catch {
    return props.page.url
  }
})
</script>

<template>
  <div class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_360px]">
    <!-- The page -->
    <div class="card flex min-w-0 flex-col overflow-hidden">
      <div
        class="flex h-10 items-center gap-2 border-b border-gray-150 bg-gray-25 px-3 text-small"
      >
        <MousePointerClick
          v-if="isJSON"
          :size="15"
          class="shrink-0 text-brand-600"
        />
        <div
          v-else
          class="flex shrink-0 rounded-md bg-gray-150/80 p-0.5"
          role="group"
          :aria-label="t('picker.modes')"
        >
          <button
            v-for="m in modes"
            :key="m.id"
            type="button"
            :aria-pressed="mode === m.id"
            :aria-label="t(m.label)"
            :title="t(m.label)"
            class="flex h-7 items-center gap-1.5 rounded-[5px] px-2 text-small transition-colors sm:px-2.5"
            :class="
              mode === m.id
                ? 'bg-raised font-medium text-gray-900 shadow-card'
                : 'text-gray-600 hover:text-gray-900'
            "
            @click="setMode(m.id)"
          >
            <component
              :is="m.icon"
              :size="14"
              :class="
                mode === m.id
                  ? m.id === 'remove'
                    ? 'text-danger-600'
                    : 'text-brand-600'
                  : ''
              "
            /><span class="hidden sm:inline">{{ t(m.label) }}</span>
          </button>
        </div>
        <span
          class="min-w-0 flex-1 truncate text-gray-600"
          :title="
            isJSON
              ? undefined
              : mode === 'remove'
                ? t('picker.removeHint')
                : t('picker.hint')
          "
          >{{
            isJSON
              ? t('picker.jsonHint')
              : mode === 'remove'
                ? t('picker.removeHint')
                : t('picker.hint')
          }}</span
        >
        <a
          :href="page.url"
          target="_blank"
          rel="noopener noreferrer"
          class="flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-gray-600 hover:bg-gray-100 hover:text-gray-900"
          :title="t('picker.openOriginal')"
          ><span class="hidden max-w-48 truncate sm:inline">{{ host }}</span
          ><ExternalLink :size="13"
        /></a>
      </div>
      <div
        v-if="!isJSON && hiddenCount"
        class="flex items-center gap-2 border-b border-gray-150 px-3 py-1.5 text-small text-gray-600"
        role="status"
      >
        <Eraser :size="14" class="shrink-0 text-gray-400" />
        <span class="min-w-0 flex-1 truncate">{{
          onlyAuto
            ? t('picker.autoHidden', { n: autoHidden })
            : t('picker.hiddenCount', { n: hiddenCount })
        }}</span>
        <button type="button" class="btn-subtle h-7 px-2" @click="unhide">
          {{ onlyAuto ? t('picker.showAgain') : t('picker.undo') }}
        </button>
      </div>
      <iframe
        v-if="!isJSON"
        ref="frame"
        :key="page.snapshot"
        :src="snapshotURL(page.snapshot)"
        sandbox="allow-scripts"
        referrerpolicy="no-referrer"
        :title="t('picker.frameTitle', { host })"
        class="h-[min(72vh,760px)] w-full bg-white"
      />
      <div
        v-else-if="page.jsonTooBig"
        class="flex h-64 items-center justify-center px-6 text-center text-sm text-gray-600"
      >
        {{ t('picker.tooBig') }}
      </div>
      <div v-else class="h-[min(72vh,760px)] overflow-auto p-3">
        <JsonTree
          :value="page.json"
          path=""
          :selected="jsonPath"
          @pick="pickPath"
        />
      </div>
    </div>

    <!-- What was picked, and what the server reads there -->
    <aside class="flex min-w-0 flex-col gap-4">
      <div class="card p-4">
        <h3 class="text-small font-semibold text-gray-900">
          {{ t('picker.selected') }}
        </h3>
        <nav
          v-if="crumbs.length"
          :aria-label="t('picker.path')"
          class="mt-2 flex flex-wrap items-center gap-1"
        >
          <template v-for="(c, i) in crumbs" :key="c.depth">
            <span v-if="i" class="text-gray-400" aria-hidden="true">›</span>
            <button
              type="button"
              class="max-w-full truncate rounded px-1.5 py-0.5 font-mono text-xs"
              :class="
                c.depth === picked?.depth
                  ? 'bg-brand-50 text-brand-700 ring-1 ring-brand-200'
                  : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900'
              "
              :aria-current="c.depth === picked?.depth ? 'true' : undefined"
              :title="t('picker.selectThis')"
              @click="selectDepth(c.depth)"
            >
              {{ c.label }}
            </button>
          </template>
        </nav>
        <p
          v-else-if="!selector && !jsonPath"
          class="mt-1 text-small text-gray-500"
        >
          {{ t('picker.none') }}
        </p>

        <template v-if="!isJSON">
          <label class="mt-3 block">
            <span class="field-label">{{ t('picker.selector') }}</span>
            <input
              v-model="selector"
              class="field font-mono text-xs"
              spellcheck="false"
              autocomplete="off"
              :placeholder="'span.price'"
              @input="onSelectorInput"
            />
          </label>
          <p
            v-if="matchText"
            class="field-hint"
            :class="{
              'text-warning-700':
                highlight && (!highlight.valid || highlight.count !== 1),
            }"
          >
            {{ matchText }}
          </p>
          <fieldset v-if="picked && picked.attrs.length" class="mt-3">
            <legend class="field-label">{{ t('picker.read') }}</legend>
            <div class="grid gap-1">
              <label
                v-for="option in [
                  { name: '', value: picked.text },
                  ...picked.attrs,
                ]"
                :key="option.name"
                class="flex min-w-0 cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-small hover:bg-gray-50"
              >
                <input
                  v-model="attr"
                  type="radio"
                  :value="option.name"
                  class="accent-brand-600"
                  @change="commit"
                />
                <span class="shrink-0 text-gray-700">{{
                  option.name ? option.name : t('picker.text')
                }}</span>
                <span
                  class="min-w-0 truncate font-mono text-xs text-gray-500"
                  >{{ option.value }}</span
                >
              </label>
            </div>
          </fieldset>
        </template>
        <label v-else class="mt-3 block">
          <span class="field-label">{{ t('picker.jsonPath') }}</span>
          <input
            v-model="jsonPath"
            class="field font-mono text-xs"
            spellcheck="false"
            autocomplete="off"
            placeholder="data.items[0].price"
            @input="onPathInput"
          />
        </label>

        <div class="mt-4 border-t border-gray-150 pt-3" aria-live="polite">
          <p class="text-xs font-medium text-gray-500">
            {{ t('picker.serverReads') }}
          </p>
          <div v-if="expired" class="message-bar-warning mt-2">
            <CircleAlert :size="16" class="mt-0.5 shrink-0 text-warning-600" />
            {{ t('picker.expired') }}
          </div>
          <p v-else-if="testing && !reading" class="mt-1 text-sm text-gray-500">
            {{ t('picker.checking') }}
          </p>
          <template v-else-if="reading?.price">
            <p class="mt-1 flex items-center gap-2">
              <CircleCheck :size="18" class="shrink-0 text-success-600" />
              <span
                class="text-title font-semibold text-gray-900 tabular-nums"
                >{{
                  formatMoney(reading.price.cents, reading.price.currency)
                }}</span
              >
              <span v-if="!reading.price.currency" class="badge-warning">{{
                t('picker.noCurrency')
              }}</span>
            </p>
            <p
              class="mt-1 truncate font-mono text-xs text-gray-500"
              :title="reading.raw"
            >
              “{{ reading.raw }}”
            </p>
          </template>
          <p
            v-else-if="reading?.error"
            class="mt-1 flex items-start gap-1.5 text-small text-danger-700"
          >
            <CircleAlert :size="15" class="mt-0.5 shrink-0" />{{
              reading.error
            }}
          </p>
          <p v-else class="mt-1 text-sm text-gray-400">—</p>
        </div>
      </div>

      <div v-if="page.suggestions.length" class="card p-4">
        <h3
          class="flex items-center gap-1.5 text-small font-semibold text-gray-900"
        >
          <Sparkles :size="15" class="text-brand-600" />{{
            t('picker.suggestions')
          }}
        </h3>
        <p class="mt-0.5 text-xs text-gray-500">
          {{ t('picker.suggestionsLead') }}
        </p>
        <ul class="mt-3 grid gap-1.5">
          <li
            v-for="s in page.suggestions"
            :key="s.kind + s.label"
            class="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5"
            :class="
              inUse(s)
                ? 'bg-brand-50 ring-1 ring-brand-100'
                : 'hover:bg-gray-50'
            "
            @mouseenter="previewSuggestion(s)"
            @mouseleave="previewSuggestion(null)"
          >
            <div class="min-w-0 flex-1">
              <p class="text-xs text-gray-500">
                {{ tKey(`picker.kinds.${s.kind}`, s.kind) }}
              </p>
              <p
                class="truncate font-mono text-xs text-gray-800"
                :title="s.label"
              >
                {{ s.label }}
              </p>
            </div>
            <span
              class="shrink-0 text-sm font-medium text-gray-900 tabular-nums"
              >{{ formatMoney(s.price.cents, s.price.currency) }}</span
            >
            <button
              type="button"
              class="btn-secondary h-7 px-2 text-xs"
              :disabled="inUse(s)"
              @click="useSuggestion(s)"
            >
              {{ inUse(s) ? t('picker.inUse') : t('picker.use') }}
            </button>
          </li>
        </ul>
      </div>

      <div
        v-if="!isJSON"
        class="rounded-md bg-gray-100/70 px-3 py-2.5 text-xs text-gray-600"
      >
        <button
          type="button"
          class="flex w-full items-center gap-1.5 text-left font-medium text-gray-700"
          :aria-expanded="showHint"
          @click="showHint = !showHint"
        >
          <Info :size="14" class="shrink-0" />{{ t('picker.jsTitle') }}
        </button>
        <p v-if="showHint" class="mt-1.5">{{ t('picker.jsBody') }}</p>
      </div>
    </aside>
  </div>
</template>
