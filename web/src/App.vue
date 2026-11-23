<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Bell,
  CircleAlert,
  Gem,
  LayoutGrid,
  Lock,
  LogIn,
  LogOut,
  Radar,
  RefreshCw,
  SlidersHorizontal,
  TrendingDown,
} from 'lucide-vue-next'
import AdminOverview from './components/AdminOverview.vue'
import AppFooter from './components/AppFooter.vue'
import GemsView from './components/GemsView.vue'
import MatchesView from './components/MatchesView.vue'
import SearchEditor from './components/SearchEditor.vue'
import SearchesView from './components/SearchesView.vue'
import WatchEditor from './components/WatchEditor.vue'
import WatchesView from './components/WatchesView.vue'
import PreferencesMenu from './components/PreferencesMenu.vue'
import SignInCard from './components/SignInCard.vue'
import ToastHost from './components/ToastHost.vue'
import { api, ApiError, type Me } from './api'
import { t } from './i18n'
import { navigate, onLinkClick, route } from './router'
import { alertLang, alertLangs } from './state'

const me = ref<Me | null>(null)
const state = ref<'loading' | 'out' | 'in' | 'guest' | 'down'>('loading')
// Signed out, the gem finder still works (when the server has one); the
// other pages ask to sign in. A guest stays one across reloads.
const guestGems = ref(false)
const GUEST_KEY = 'guest'
const admin = computed(() => me.value?.admin ?? false)
const gems = computed(() => me.value?.gems ?? guestGems.value)
const locked = (id: string) => state.value === 'guest' && id !== 'gems'
// The sidebar entry for the current page; an editor belongs to its list.
const tab = computed(() =>
  route.value.name === 'search'
    ? 'searches'
    : route.value.name === 'watch'
      ? 'watches'
      : route.value.name,
)
const expired = new URLSearchParams(location.search).get('login') === 'expired'

const navigation = computed(() => [
  {
    id: 'searches' as const,
    href: '/searches',
    label: 'app.searches' as const,
    icon: SlidersHorizontal,
  },
  {
    id: 'watches' as const,
    href: '/watches',
    label: 'app.watches' as const,
    icon: TrendingDown,
  },
  {
    id: 'alerts' as const,
    href: '/alerts',
    label: 'app.alerts' as const,
    icon: Bell,
  },
  // Only when the server has a source to scan.
  ...(gems.value
    ? [
        {
          id: 'gems' as const,
          href: '/gems',
          label: 'app.gems' as const,
          icon: Gem,
        },
      ]
    : []),
])
const adminNavigation = [
  {
    id: 'overview' as const,
    href: '/overview',
    label: 'app.overview' as const,
    icon: LayoutGrid,
  },
]
const initials = computed(() =>
  (me.value?.name ?? '')
    .split(/[\s._-]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase())
    .join(''),
)

onMounted(async () => {
  try {
    me.value = await api.me()
    alertLangs.value = me.value.alertLangs
    alertLang.value = me.value.alertLang
    state.value = 'in'
    localStorage.removeItem(GUEST_KEY)
  } catch (reason) {
    state.value =
      reason instanceof ApiError && reason.status === 401 ? 'out' : 'down'
  }
  if (expired) history.replaceState(null, '', '/')
  if (state.value !== 'out') return
  guestGems.value = (await api.guest().catch(() => ({ gems: false }))).gems
  // A shared gem finder link opens for anyone.
  if (
    guestGems.value &&
    (route.value.name === 'gems' || localStorage.getItem(GUEST_KEY))
  )
    continueAsGuest()
})

function continueAsGuest() {
  localStorage.setItem(GUEST_KEY, '1')
  state.value = 'guest'
  if (route.value.name !== 'gems') navigate('/gems', { replace: true })
}

function signIn() {
  localStorage.removeItem(GUEST_KEY)
  state.value = 'out'
}

function reload() {
  location.reload()
}

async function logout() {
  await api.logout().catch(() => undefined)
  localStorage.removeItem(GUEST_KEY)
  guestGems.value = me.value?.gems ?? false
  me.value = null
  alertLang.value = null
  state.value = 'out'
}
</script>

<template>
  <!-- Signed in or a guest: sidebar on wide screens, top bar on narrow ones. -->
  <div
    v-if="(state === 'in' && me) || state === 'guest'"
    class="flex min-h-screen flex-col lg:flex-row"
  >
    <aside
      class="sticky top-0 z-20 hidden h-screen w-64 shrink-0 flex-col border-r border-gray-150 bg-gray-100/70 lg:flex"
    >
      <a
        href="/searches"
        class="flex h-16 items-center gap-2.5 px-5"
        @click="onLinkClick($event, '/searches')"
      >
        <span
          class="grid size-7 place-items-center rounded-md bg-brand-600 text-white shadow-[0_1px_2px_rgb(15_108_189/0.3)]"
          ><Radar :size="16"
        /></span>
        <span class="text-sm font-semibold text-gray-900">{{
          t('app.name')
        }}</span>
      </a>

      <nav :aria-label="t('app.mainNav')" class="flex-1 px-3 pt-2">
        <template
          v-for="group in admin
            ? [
                { label: '', items: navigation },
                { label: t('app.administration'), items: adminNavigation },
              ]
            : [{ label: '', items: navigation }]"
          :key="group.label"
        >
          <p
            v-if="group.label"
            class="mt-6 mb-1.5 px-3 text-xs font-medium text-gray-500"
          >
            {{ group.label }}
          </p>
          <a
            v-for="item in group.items"
            :href="item.href"
            :key="item.id"
            :aria-current="tab === item.id ? 'page' : undefined"
            class="relative mb-0.5 flex h-9 w-full items-center gap-3 rounded-md px-3 text-sm transition-colors duration-100"
            :class="
              tab === item.id
                ? 'bg-raised font-medium text-gray-900 shadow-card'
                : 'text-gray-700 hover:bg-gray-150/70 hover:text-gray-900'
            "
            @click="onLinkClick($event, item.href)"
          >
            <span
              v-if="tab === item.id"
              class="absolute top-1/2 left-0 h-4 w-[3px] -translate-y-1/2 rounded-full bg-brand-600"
              aria-hidden="true"
            />
            <component
              :is="item.icon"
              :size="18"
              :class="tab === item.id ? 'text-brand-600' : 'text-gray-500'"
            />{{ t(item.label) }}
            <Lock
              v-if="locked(item.id)"
              :size="14"
              class="ml-auto text-gray-400"
              aria-hidden="true"
            />
          </a>
        </template>
      </nav>

      <div
        v-if="!me"
        class="flex items-center gap-3 border-t border-gray-150 px-4 py-3"
      >
        <p class="min-w-0 flex-1 text-sm font-medium text-gray-900">
          {{ t('app.guest') }}
        </p>
        <PreferencesMenu placement="up" />
        <button class="btn-secondary -ml-1" @click="signIn">
          <LogIn :size="14" />{{ t('app.signIn') }}
        </button>
      </div>
      <div
        v-else
        class="flex items-center gap-3 border-t border-gray-150 px-4 py-3"
      >
        <span
          class="grid size-8 shrink-0 place-items-center rounded-full bg-brand-100 text-xs font-semibold text-brand-700"
          aria-hidden="true"
          >{{ initials }}</span
        >
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm font-medium text-gray-900">
            {{ me.name }}
          </p>
          <p class="text-xs text-gray-500">
            {{ me.admin ? t('app.administrator') : t('app.member') }}
          </p>
        </div>
        <PreferencesMenu placement="up" />
        <button
          class="btn-icon -ml-1"
          :aria-label="t('app.signOut')"
          :title="t('app.signOut')"
          @click="logout"
        >
          <LogOut :size="16" />
        </button>
      </div>
    </aside>

    <header
      class="sticky top-0 z-10 border-b border-gray-150 bg-surface/90 backdrop-blur-md lg:hidden"
    >
      <div class="flex h-14 items-center gap-2.5 px-4">
        <span
          class="grid size-7 place-items-center rounded-md bg-brand-600 text-white"
          ><Radar :size="16"
        /></span>
        <span class="flex-1 text-sm font-semibold">{{ t('app.name') }}</span>
        <PreferencesMenu />
        <button
          v-if="!me"
          class="btn-icon"
          :aria-label="t('app.signIn')"
          :title="t('app.signIn')"
          @click="signIn"
        >
          <LogIn :size="16" />
        </button>
        <button
          v-else
          class="btn-icon"
          :aria-label="t('app.signOut')"
          :title="t('app.signOut')"
          @click="logout"
        >
          <LogOut :size="16" />
        </button>
      </div>
      <nav
        :aria-label="t('app.mainNav')"
        class="flex gap-5 overflow-x-auto px-4"
      >
        <a
          v-for="item in admin
            ? [...navigation, ...adminNavigation]
            : navigation"
          :href="item.href"
          :key="item.id"
          :aria-current="tab === item.id ? 'page' : undefined"
          class="relative flex h-10 shrink-0 items-center text-sm transition-colors"
          :class="
            tab === item.id
              ? 'font-medium text-gray-900'
              : 'text-gray-600 hover:text-gray-900'
          "
          @click="onLinkClick($event, item.href)"
        >
          {{ t(item.label) }}
          <Lock
            v-if="locked(item.id)"
            :size="12"
            class="ml-1 text-gray-400"
            aria-hidden="true"
          />
          <span
            v-if="tab === item.id"
            class="absolute inset-x-0 bottom-0 h-[3px] rounded-full bg-brand-600"
            aria-hidden="true"
          />
        </a>
      </nav>
    </header>

    <!-- A column at least the window's height, so an editor's action bar
         sits at the bottom even when the form is short. -->
    <main class="flex min-w-0 flex-1 flex-col overflow-x-clip">
      <div
        class="mx-auto flex w-full max-w-[1200px] flex-1 flex-col px-4 py-8 sm:px-8 lg:px-10 lg:py-10"
      >
        <GemsView v-if="route.name === 'gems' && gems" :admin="admin" />
        <template v-else-if="me">
          <SearchEditor
            v-if="route.name === 'search'"
            :me="me"
            :id="route.id"
          />
          <WatchEditor
            v-else-if="route.name === 'watch'"
            :me="me"
            :id="route.id"
          />
          <WatchesView v-else-if="route.name === 'watches'" :me="me" />
          <MatchesView v-else-if="route.name === 'alerts'" />
          <AdminOverview v-else-if="route.name === 'overview' && me.admin" />
          <SearchesView v-else :me="me" />
        </template>
        <SignInCard v-else locked class="mx-auto mt-8 w-full max-w-[400px]" />
      </div>
      <AppFooter />
    </main>
  </div>

  <!-- Signed out, unreachable or still checking: a single centered card. -->
  <main
    v-else
    class="flex min-h-screen flex-col bg-gray-50"
    :aria-busy="state === 'loading'"
  >
    <div class="grid flex-1 place-items-center px-4 py-16">
      <div
        v-if="state === 'loading'"
        class="size-6 animate-spin rounded-full border-2 border-gray-200 border-t-brand-600"
        role="status"
        :aria-label="t('common.loading')"
      />

      <div v-else class="w-full max-w-[400px]">
        <div class="fixed top-3 right-3">
          <PreferencesMenu />
        </div>
        <div class="mb-6 flex items-center justify-center gap-2.5">
          <span
            class="grid size-8 place-items-center rounded-lg bg-brand-600 text-white shadow-[0_1px_2px_rgb(15_108_189/0.3)]"
            ><Radar :size="18"
          /></span>
          <span class="text-base font-semibold">{{ t('app.name') }}</span>
        </div>

        <SignInCard v-if="state === 'out'" :expired="expired">
          <button
            v-if="guestGems"
            class="btn-secondary mt-4 w-full justify-center"
            @click="continueAsGuest"
          >
            <Gem :size="14" />{{ t('signIn.guest') }}
          </button>
        </SignInCard>

        <section v-else role="alert" class="card p-8 text-center">
          <span
            class="mx-auto mb-4 grid size-10 place-items-center rounded-full bg-danger-50 text-danger-600"
            ><CircleAlert :size="20"
          /></span>
          <h1 class="text-base font-semibold">{{ t('down.title') }}</h1>
          <p class="mt-1 text-sm text-gray-600">
            {{ t('down.body') }}
          </p>
          <button class="btn-secondary mt-6" @click="reload">
            <RefreshCw :size="14" />{{ t('common.tryAgain') }}
          </button>
        </section>
      </div>
    </div>
    <AppFooter v-if="state !== 'loading'" />
  </main>
  <ToastHost />
</template>
