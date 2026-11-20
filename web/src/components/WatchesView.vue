<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  CircleAlert,
  Pencil,
  Plus,
  Trash2,
  TrendingDown,
  TriangleAlert,
} from 'lucide-vue-next'
import BaseDialog from './BaseDialog.vue'
import EmptyState from './EmptyState.vue'
import PageHeader from './PageHeader.vue'
import ToggleSwitch from './ToggleSwitch.vue'
import { api, ApiError, type Me, type Watch } from '../api'
import { formatMoney, formatRelative, t } from '../i18n'
import { navigate, onLinkClick } from '../router'
import { toast, toastError } from '../toast'
import { showEveryone as everyone } from '../state'
import { changePercent, formatChange, hostOf, ruleText } from '../watches'

const props = defineProps<{ me: Me }>()

const watches = ref<Watch[]>([])
const deleting = ref<Watch | null>(null)
const error = ref('')
const loading = ref(true)

const atLimit = computed(
  () =>
    !props.me.admin &&
    props.me.maxWatches > 0 &&
    watches.value.length >= props.me.maxWatches,
)

async function load() {
  error.value = ''
  try {
    watches.value = await api.watches(everyone.value)
  } catch (reason) {
    error.value =
      reason instanceof ApiError ? reason.message : t('watches.loadFailed')
  } finally {
    loading.value = false
  }
}

// Runs an action, says how it went, and reloads the list from the server.
async function act(fn: () => Promise<void>, done: string) {
  try {
    await fn()
    toast(done)
  } catch (reason) {
    toastError(
      reason instanceof ApiError ? reason.message : t('watches.actionFailed'),
    )
  }
  await load()
}

function setEnabled(w: Watch, enabled: boolean) {
  w.enabled = enabled
  void act(
    () => api.setWatchEnabled(w.id, enabled),
    t(enabled ? 'toast.watchResumed' : 'toast.watchPaused', { name: w.name }),
  )
}

function confirmDelete() {
  const w = deleting.value
  deleting.value = null
  if (w)
    void act(
      () => api.deleteWatch(w.id),
      t('toast.watchDeleted', { name: w.name }),
    )
}

function showEveryone(value: boolean) {
  if (everyone.value === value) return
  everyone.value = value
  loading.value = true
  void load()
}

onMounted(load)
</script>

<template>
  <section>
    <PageHeader :title="t('watches.title')">
      <template #description>
        {{ t('watches.lead') }}
        <template v-if="me.maxWatches && !me.admin">
          <span class="text-gray-400"> · </span
          ><span class="tabular-nums">{{
            t('watches.used', { used: watches.length, max: me.maxWatches })
          }}</span></template
        >
      </template>
      <div
        v-if="me.admin"
        class="flex rounded-md bg-gray-150/80 p-0.5"
        role="group"
        :aria-label="t('watches.whose')"
      >
        <button
          v-for="option in [
            { value: false, label: t('searches.mine') },
            { value: true, label: t('searches.everyone') },
          ]"
          :key="option.label"
          :aria-pressed="everyone === option.value"
          class="h-7 rounded-[5px] px-3 text-small transition-colors"
          :class="
            everyone === option.value
              ? 'bg-raised font-medium text-gray-900 shadow-card'
              : 'text-gray-600 hover:text-gray-900'
          "
          @click="showEveryone(option.value)"
        >
          {{ option.label }}
        </button>
      </div>
      <button
        class="btn-primary"
        :disabled="atLimit"
        :title="atLimit ? t('watches.atLimit') : undefined"
        @click="navigate('/watches/new')"
      >
        <Plus :size="16" />{{ t('watches.new') }}
      </button>
    </PageHeader>

    <div v-if="error" role="alert" class="message-bar-danger mb-6">
      <CircleAlert :size="16" class="mt-0.5 shrink-0 text-danger-600" />{{
        error
      }}
    </div>

    <div class="card overflow-hidden">
      <div
        v-if="loading"
        class="divide-y divide-gray-100"
        role="status"
        :aria-label="t('watches.loading')"
      >
        <div v-for="n in 3" :key="n" class="flex items-center gap-6 px-5 py-4">
          <div class="skeleton size-10 rounded-md" />
          <div class="flex-1">
            <div class="skeleton h-4 w-48" />
            <div class="skeleton mt-2 h-3 w-64 max-w-full" />
          </div>
          <div class="skeleton h-5 w-20" />
        </div>
      </div>

      <EmptyState
        v-else-if="!watches.length"
        :icon="TrendingDown"
        :title="
          everyone ? t('watches.emptyEveryoneTitle') : t('watches.emptyTitle')
        "
        :description="t('watches.emptyBody')"
      >
        <button class="btn-primary" @click="navigate('/watches/new')">
          <Plus :size="16" />{{ t('watches.new') }}
        </button>
      </EmptyState>

      <template v-else>
        <div
          class="hidden grid-cols-[minmax(0,1fr)_130px_210px_120px_64px_80px] items-center border-b border-gray-150 bg-gray-25 px-4 md:grid"
        >
          <span class="table-head pl-0!">{{ t('watches.colWatch') }}</span>
          <span class="table-head text-right">{{ t('watches.colPrice') }}</span>
          <span class="table-head">{{ t('watches.colRule') }}</span>
          <span class="table-head">{{ t('watches.colChecked') }}</span>
          <span class="table-head">{{ t('watches.colActive') }}</span>
          <span class="table-head"
            ><span class="sr-only">{{ t('watches.colActions') }}</span></span
          >
        </div>
        <ul class="divide-y divide-gray-100">
          <li
            v-for="w in watches"
            :key="w.id"
            class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 px-4 py-3.5 transition-colors hover:bg-gray-25 md:grid-cols-[minmax(0,1fr)_130px_210px_120px_64px_80px] md:gap-0 md:py-3"
          >
            <div class="flex min-w-0 items-center gap-3 md:pr-4">
              <img
                v-if="w.imageUrl"
                :src="w.imageUrl"
                alt=""
                referrerpolicy="no-referrer"
                loading="lazy"
                class="size-10 shrink-0 rounded-md bg-gray-100 object-cover"
              />
              <span
                v-else
                class="grid size-10 shrink-0 place-items-center rounded-md bg-gray-100 text-gray-400"
                ><TrendingDown :size="18"
              /></span>
              <div class="min-w-0">
                <div class="flex min-w-0 items-center gap-2">
                  <a
                    :href="`/watches/${w.id}`"
                    class="truncate font-medium hover:text-brand-700 hover:underline hover:underline-offset-2"
                    :class="w.enabled ? 'text-gray-900' : 'text-gray-500'"
                    @click="onLinkClick($event, `/watches/${w.id}`)"
                    >{{ w.name }}</a
                  >
                  <span v-if="!w.enabled" class="badge-warning">{{
                    t('common.paused')
                  }}</span>
                  <span v-else-if="w.below" class="badge-success">{{
                    t('watches.belowNow')
                  }}</span>
                </div>
                <p class="mt-0.5 truncate text-small text-gray-500">
                  <span v-if="everyone" class="text-gray-700"
                    >{{ w.userName }} ·
                  </span>
                  {{ hostOf(w.url) }}
                </p>
              </div>
            </div>

            <div
              class="col-span-2 flex items-baseline gap-2 md:col-span-1 md:block md:px-4 md:text-right"
            >
              <p class="font-medium text-gray-900 tabular-nums">
                {{
                  w.priceCents === null
                    ? '—'
                    : formatMoney(w.priceCents, w.currency)
                }}
              </p>
              <p
                v-if="changePercent(w) !== null"
                class="text-xs tabular-nums"
                :class="
                  changePercent(w)! < 0 ? 'text-success-700' : 'text-gray-500'
                "
                :title="t('watches.sinceReference')"
              >
                {{ formatChange(changePercent(w)!) }}
              </p>
            </div>

            <p
              class="col-span-2 text-small text-gray-700 md:col-span-1 md:px-4"
            >
              <span class="text-gray-500 md:hidden"
                >{{ t('watches.colRule') }}:
              </span>
              {{ ruleText(w.rule, w.thresholdCents, w.currency) }}
            </p>

            <div class="col-span-2 text-small md:col-span-1 md:px-4">
              <span v-if="w.failing" class="badge-warning" :title="w.lastError"
                ><TriangleAlert :size="12" />{{ t('watches.failing') }}</span
              >
              <span
                v-else-if="w.failures"
                class="text-warning-700"
                :title="w.lastError"
                >{{ t('watches.retrying') }}</span
              >
              <span v-else class="text-gray-600">{{
                w.checkedAt ? formatRelative(w.checkedAt) : t('watches.never')
              }}</span>
            </div>

            <div
              class="col-start-2 row-start-1 flex items-center gap-1 md:contents"
            >
              <div class="md:px-4">
                <ToggleSwitch
                  :model-value="w.enabled"
                  :label="
                    w.enabled
                      ? t('watches.pause', { name: w.name })
                      : t('watches.resume', { name: w.name })
                  "
                  @update:model-value="setEnabled(w, $event)"
                />
              </div>
              <div class="flex items-center justify-end gap-0.5">
                <a
                  class="btn-icon"
                  :href="`/watches/${w.id}`"
                  :aria-label="t('watches.editNamed', { name: w.name })"
                  :title="t('common.edit')"
                  @click="onLinkClick($event, `/watches/${w.id}`)"
                >
                  <Pencil :size="16" />
                </a>
                <button
                  class="btn-icon hover:bg-danger-50 hover:text-danger-600"
                  :aria-label="t('watches.deleteNamed', { name: w.name })"
                  :title="t('common.delete')"
                  @click="deleting = w"
                >
                  <Trash2 :size="16" />
                </button>
              </div>
            </div>
          </li>
        </ul>
      </template>
    </div>

    <BaseDialog
      v-if="deleting"
      variant="modal"
      :title="t('watches.deleteTitle')"
      light-dismiss
      @close="deleting = null"
    >
      <p class="text-sm text-gray-600">
        {{ t('watches.deleteBody', { name: deleting.name }) }}
      </p>
      <template #footer>
        <button class="btn-secondary" @click="deleting = null">
          {{ t('common.cancel') }}
        </button>
        <button class="btn-danger" @click="confirmDelete">
          {{ t('common.delete') }}
        </button>
      </template>
    </BaseDialog>
  </section>
</template>
