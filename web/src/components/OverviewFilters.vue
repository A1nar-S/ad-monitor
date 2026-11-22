<script setup lang="ts">
import { Search, X } from 'lucide-vue-next'
import { categoryName, t } from '../i18n'

// The overview's filters, laid out as the gem finder's: a search box and
// chips. The parent owns every value and reloads when one changes.
defineProps<{ view: 'listings' | 'searches'; sources: string[] }>()
const query = defineModel<string>('query', { required: true })
const category = defineModel<string>('category', { required: true })
const status = defineModel<string>('status', { required: true })
const source = defineModel<string>('source', { required: true })

const categories = ['all', 'cars', 'flats', 'parts']
</script>

<template>
  <div class="space-y-5">
    <label class="block">
      <span class="field-label">{{ t('overview.searchRecords') }}</span>
      <span class="field-within flex h-8 items-center gap-2 pr-1 pl-2.5">
        <Search :size="15" class="shrink-0 text-gray-400" />
        <input
          v-model="query"
          class="h-full w-full min-w-0 bg-transparent text-sm outline-none placeholder:text-gray-400 focus-visible:outline-none"
          :placeholder="
            view === 'listings'
              ? t('overview.searchListings')
              : t('overview.searchSearches')
          "
        />
        <button
          v-if="query"
          type="button"
          class="grid size-6 shrink-0 place-items-center rounded text-gray-500 hover:bg-gray-100 hover:text-gray-900"
          :aria-label="t('overview.clearSearch')"
          :title="t('overview.clearSearch')"
          @click.prevent="query = ''"
        >
          <X :size="14" />
        </button>
      </span>
    </label>

    <fieldset v-if="view === 'listings'">
      <legend class="field-label">{{ t('overview.category') }}</legend>
      <div class="flex flex-wrap gap-1.5">
        <button
          v-for="c in categories"
          :key="c"
          type="button"
          class="chip"
          :aria-pressed="category === c"
          @click="category = c"
        >
          {{ c === 'all' ? t('overview.all') : categoryName(c) }}
        </button>
      </div>
    </fieldset>

    <fieldset>
      <legend class="field-label">{{ t('overview.status') }}</legend>
      <div class="flex flex-wrap gap-1.5">
        <button
          v-for="s in ['all', 'active', 'inactive']"
          :key="s"
          type="button"
          class="chip"
          :aria-pressed="status === s"
          @click="status = s"
        >
          {{
            s === 'all'
              ? t('overview.anyStatus')
              : s === 'active'
                ? view === 'listings'
                  ? t('common.active')
                  : t('common.enabled')
                : view === 'listings'
                  ? t('common.inactive')
                  : t('common.paused')
          }}
        </button>
      </div>
    </fieldset>

    <fieldset v-if="sources.length > 1">
      <legend class="field-label">{{ t('overview.source') }}</legend>
      <div class="flex flex-wrap gap-1.5">
        <button
          v-for="s in ['all', ...sources]"
          :key="s"
          type="button"
          class="chip"
          :aria-pressed="source === s"
          @click="source = s"
        >
          {{ s === 'all' ? t('overview.allSources') : s }}
        </button>
      </div>
    </fieldset>
  </div>
</template>
