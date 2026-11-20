import { fieldLabel, t } from './i18n'

// Typed client for the Go API (internal/web). The session is an HttpOnly
// cookie; every change carries X-Requested-With, which the API requires.

export type Me = {
  name: string
  platform: string
  admin: boolean
  maxSearches: number
  maxWatches: number
  // Check intervals a price watch may use, in minutes.
  watchIntervals: number[]
}

export type FieldSpec = {
  key: string
  label: string
  kind: 'multi' | 'all' | 'range' | 'text'
  required?: boolean
  detail?: boolean
  options?: string[]
  dependsOn?: string
  optionsBy?: Record<string, string[]>
}

export type Category = { id: string; label: string; fields: FieldSpec[] }

export type Channel = {
  id: number
  kind: string
  target: string
  label: string
}

export type Range = { min?: number; max?: number }
export type Filters = Record<string, string[] | Range>

export type Search = {
  id: number
  userId: number
  userName: string
  name: string
  enabled: boolean
  source: string
  category: string
  filters: Filters
  feeds: string[]
  channel: Channel
  matches: number
  createdAt: string
  updatedAt: string
}

export type SearchInput = {
  name: string
  category: string
  filters: Filters
  channelId: number
}

export type Match = {
  searchId: number
  searchName: string
  source: string
  externalId: string
  at: string
  listing?: {
    title: string
    url: string
    price: string
    imageUrl: string
    attrs: Record<string, unknown>
    active: boolean
  }
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

async function call<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(path, {
    method,
    signal,
    credentials: 'same-origin',
    headers: {
      'X-Requested-With': 'fetch',
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (response.status === 204) return undefined as T
  const data = await response.json().catch(() => ({}))
  if (!response.ok)
    throw new ApiError(
      response.status,
      data.error ?? `Request failed (${response.status})`,
    )
  return data as T
}

export const api = {
  me: () => call<Me>('GET', '/api/me'),
  logout: () => call<void>('POST', '/api/logout'),
  categories: () => call<Category[]>('GET', '/api/categories'),
  channels: () => call<Channel[]>('GET', '/api/channels'),
  searches: (all = false) =>
    call<Search[]>('GET', `/api/searches${all ? '?all=1' : ''}`),
  createSearch: (input: SearchInput) =>
    call<Search>('POST', '/api/searches', input),
  updateSearch: (id: number, input: SearchInput) =>
    call<Search>('PUT', `/api/searches/${id}`, input),
  setEnabled: (id: number, enabled: boolean) =>
    call<void>('PATCH', `/api/searches/${id}`, { enabled }),
  deleteSearch: (id: number) => call<void>('DELETE', `/api/searches/${id}`),
  matches: (limit = 50) => call<Match[]>('GET', `/api/matches?limit=${limit}`),

  watches: (all = false) =>
    call<Watch[]>('GET', `/api/watches${all ? '?all=1' : ''}`),
  watch: (id: number) => call<WatchDetail>('GET', `/api/watches/${id}`),
  createWatch: (input: WatchInput) =>
    call<Watch>('POST', '/api/watches', input),
  updateWatch: (id: number, input: WatchInput) =>
    call<Watch>('PUT', `/api/watches/${id}`, input),
  setWatchEnabled: (id: number, enabled: boolean) =>
    call<void>('PATCH', `/api/watches/${id}`, { enabled }),
  deleteWatch: (id: number) => call<void>('DELETE', `/api/watches/${id}`),
  resetWatch: (id: number) => call<Watch>('POST', `/api/watches/${id}/reset`),
  checkWatch: (id: number) => call<Watch>('POST', `/api/watches/${id}/check`),

  inspectPage: (url: string, signal?: AbortSignal) =>
    call<InspectedPage>('POST', '/api/pages/inspect', { url }, signal),
  testExtractor: (
    snapshot: string,
    extractor: Extractor,
    signal?: AbortSignal,
  ) =>
    call<ExtractorTest>(
      'POST',
      '/api/pages/test',
      { snapshot, extractor },
      signal,
    ),
}

// --- price watches ------------------------------------------------------------

// Where a price is on a page: a CSS selector (its text, or one attribute),
// optionally read as JSON along jsonPath. No selector = a JSON API.
export type Extractor = {
  selector?: string
  attr?: string
  jsonPath?: string
  currency?: string
}

// Amounts are exact: cents are hundredths of the currency's unit.
export type Price = { cents: number; currency: string }

export type WatchRule =
  { kind: 'below'; targetCents: number } | { kind: 'drop'; dropPercent: number }

export type Watch = {
  id: number
  userId: number
  userName: string
  name: string
  enabled: boolean
  url: string
  extractor: Extractor
  title: string
  imageUrl: string
  rule: WatchRule
  thresholdCents: number
  referenceCents: number
  referenceAt: string
  below: boolean
  intervalMinutes: number
  channel: Channel
  alerts: number
  currency: string
  priceCents: number | null
  raw: string
  checkedAt: string | null
  okAt: string | null
  nextCheckAt: string
  failures: number
  failing: boolean
  lastError: string
  // A "check now" that found the page read under 30 s ago and did not read it again.
  skipped?: boolean
  createdAt: string
  updatedAt: string
}

export type WatchInput = {
  name: string
  url: string
  // The page copy the price was picked on; it gives the starting price.
  snapshot?: string
  extractor: Extractor
  rule: WatchRule
  intervalMinutes: number
  channelId: number
}

export type PricePoint = {
  at: string
  ok: boolean
  cents: number | null
  currency: string
  raw: string
  error: string
}

export type WatchAlert = {
  kind: 'drop' | 'failing' | 'recovered'
  cents: number | null
  at: string
  sent: boolean
}

export type WatchDetail = {
  watch: Watch
  history: PricePoint[] // newest first
  alerts: WatchAlert[]
}

export type Suggestion = {
  kind: 'structured' | 'meta' | 'field' | 'element' | 'json'
  label: string
  extractor: Extractor
  raw: string
  price: Price
}

export type InspectedPage = {
  snapshot: string
  url: string
  kind: 'html' | 'json'
  title: string
  imageUrl: string
  currency: string
  json?: unknown
  jsonTooBig?: boolean
  suggestions: Suggestion[]
  fetched: string
}

export type ExtractorTest = {
  raw: string
  price: Price | null
  matches: number
  error?: string
}

// The iframe address of an inspected page's copy, with the picker.
export const snapshotURL = (snapshot: string) =>
  `/api/pages/snapshots/${encodeURIComponent(snapshot)}`

// describe renders filters as one line: "make Skoda, Toyota · price ≤ 7000".
export function describe(filters: Filters, fields?: FieldSpec[]): string {
  const label = (key: string) =>
    fieldLabel(key, fields?.find((f) => f.key === key)?.label ?? key)
  const parts = Object.keys(filters)
    .sort()
    .map((key) => {
      const value = filters[key]
      if (Array.isArray(value)) return `${label(key)}: ${value.join(', ')}`
      if (value.min !== undefined && value.max !== undefined)
        return `${label(key)} ${value.min}–${value.max}`
      if (value.min !== undefined) return `${label(key)} ≥ ${value.min}`
      if (value.max !== undefined) return `${label(key)} ≤ ${value.max}`
      return ''
    })
    .filter(Boolean)
  return parts.length ? parts.join(' · ') : t('describe.everything')
}

// --- search preview ---------------------------------------------------------

export type PreviewAd = {
  id: number
  title: string
  url: string
  price: string
  imageUrl: string
  location: string
  firstSeen: string
  active: boolean
  // false: passes the list-page filters, but its ad page was never read to
  // check the rest (Preview.unsureKeys).
  sure: boolean
}

export type Preview = {
  scanned: number
  inScope: number
  matched: number
  unsure: number
  unsureKeys: string[]
  since?: string
  ads: PreviewAd[]
}

export const previewSearch = (
  input: { category: string; filters: Filters },
  signal?: AbortSignal,
) => call<Preview>('POST', '/api/searches/preview', input, signal)

// --- listing details (admin) ------------------------------------------------

export type ListingDetails = {
  attrs: Record<string, unknown>
  active: boolean
  detailedAt: string
}

// Reads the ad's own page on its site now and saves what it adds.
export const readListingDetails = (id: number) =>
  call<ListingDetails>('POST', `/api/listings/${id}/details`)
