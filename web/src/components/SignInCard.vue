<script setup lang="ts">
import { TriangleAlert } from 'lucide-vue-next'
import { t } from '../i18n'

// How to sign in: on the signed-out page, and in place of a page a guest
// may not open. The slot goes under the steps.
defineProps<{ expired?: boolean; locked?: boolean }>()
</script>

<template>
  <section class="card p-8">
    <h1 class="text-title font-semibold">{{ t('signIn.title') }}</h1>
    <p class="mt-1 text-sm text-gray-600">
      {{ locked ? t('signIn.locked') : t('signIn.lead') }}
    </p>
    <div v-if="expired" role="alert" class="message-bar-warning mt-5">
      <TriangleAlert :size="16" class="mt-0.5 shrink-0 text-warning-600" />
      {{ t('signIn.expired') }}
    </div>
    <ol class="mt-6 grid gap-4 text-sm">
      <li class="flex gap-3">
        <span
          class="grid size-6 shrink-0 place-items-center rounded-full bg-gray-100 text-xs font-semibold text-gray-700"
          >1</span
        >
        <span class="pt-0.5 text-gray-700"
          >{{ t('signIn.step1Before') }}
          <span class="kbd">/dashboard</span>
          {{ t('signIn.step1After') }}</span
        >
      </li>
      <li class="flex gap-3">
        <span
          class="grid size-6 shrink-0 place-items-center rounded-full bg-gray-100 text-xs font-semibold text-gray-700"
          >2</span
        >
        <span class="pt-0.5 text-gray-700">{{ t('signIn.step2') }}</span>
      </li>
    </ol>
    <p class="mt-6 border-t border-gray-150 pt-4 text-xs text-gray-500">
      {{ t('signIn.footnote') }}
    </p>
    <slot />
  </section>
</template>
