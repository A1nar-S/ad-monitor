import { fieldLabel, locale, t } from './i18n'

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
  // The gem finder is on (everyone browses; admins scan).
  gems: boolean
}

// A full scan of every listing of a category for sale, rated: the gem finder. Pass 1
// (phase listing) reads every listing; pass 2 (detailing) the ad pages of
// the potential buys.
// full reads every list page again; rate rates what is stored again.
export type GemScanMode = 'full' | 'rate'

export type GemScan = {
  id: number
  mode: GemScanMode
  status: 'requested' | 'running' | 'done' | 'failed'
  requestedAt: string
  startedAt?: string
  finishedAt?: string
  pages: number
  ads: number
  gone: number
  rated: number
  error?: string
  // The crawl reached the end and sold ones were marked; note says why not.
  complete: boolean
  note?: string
  phase?: 'listing' | 'detailing'
  detailTotal: number
  detailed: number
}

// What a category's gems carry, as filters and on the cards (gems.Field).
export type GemField = {
  key: string
  kind: 'choice' | 'range'
  // choice: select (one), chips (any, toggled), search (any, typed);
  // range: range (from and to) or max (up to).
  control: 'select' | 'chips' | 'search' | 'range' | 'max'
  dependsOn?: string
  detail?: boolean // only read ad pages have it
  unit?: 'km' | 'l'
  card?: boolean
  tones?: Record<string, 'good' | 'bad'>
  icon?: 'pin'
  options?: string[] // every value there can be, from the site's catalog
}

export type GemSpec = {
  fields: GemField[]
  sorts: { id: string; key: string; desc?: boolean }[]
}

// A site the finder covers and the categories it can be scanned for.
export type GemSource = {
  id: string
  name: string
  categories: { id: string; spec: GemSpec }[]
}

export type GemBuy = 'good' | 'average' | 'bad' | 'check'

// One part of a score: x from -1 to +1, weighted.
export type GemComponent = {
  key: string
  x: number
  weight: number
  points: number
  // points on the 0–100 scale; missing on gems rated before it existed
  score?: number
  why: string
}

export type Gem = {
  externalId: string
  heading: string
  title: string
  url: string
  imageUrl: string
  price: string
  priceValue: number
  // vs similar listings: -0.14 is 14% below; null = not compared.
  priceDiff: number | null
  buy: GemBuy
  score: number // 0-100
  overall: string
  fields: { name: string; value: string }[]
  detailed: boolean
  components: GemComponent[]
  // The spec fields' values; unknown ones are missing.
  attrs: Record<string, string | number>
}

export type GemQuery = {
  buy?: GemBuy | ''
  sort?: string
  page?: number
  minPrice?: number
  maxPrice?: number
  detailed?: boolean
  // A choice field's key → any of these values.
  choices: Record<string, string[]>
  // A range field's key → from and to.
  ranges: Record<string, { min?: number; max?: number }>
}

export type GemFacet = { value: string; parent?: string; count: number }

export type GemPage = {
  items: Gem[]
  total: number
  page: number
  pageSize: number
  // Each choice's values among the gems the other filters let through;
  // buys the same for the verdicts.
  facets: Record<string, GemFacet[]>
  buys: GemFacet[]
  // detailed of all gems had their ad page read.
  detailed: number
  all: number
}

// The query as URL parameters, for the API and the page's address alike: a
// choice repeats its key, a range is <key>_min and <key>_max, and empty
// values are left out.
export function gemParams(q: GemQuery): URLSearchParams {
  const p = new URLSearchParams()
  if (q.buy) p.set('buy', q.buy)
  if (q.sort) p.set('sort', q.sort)
  if (q.minPrice) p.set('minPrice', String(q.minPrice))
  if (q.maxPrice) p.set('maxPrice', String(q.maxPrice))
  if (q.detailed) p.set('detailed', '1')
  for (const [key, values] of Object.entries(q.choices))
    for (const v of values) p.append(key, v)
  for (const [key, r] of Object.entries(q.ranges)) {
    if (r.min) p.set(`${key}_min`, String(r.min))
    if (r.max) p.set(`${key}_max`, String(r.max))
  }
  if (q.page && q.page > 1) p.set('page', String(q.page))
  return p
}

function target(source: string, category: string) {
  return new URLSearchParams({ source, category }).toString()
}

// parseGemParams reads an address's query back for spec.
export function parseGemParams(p: URLSearchParams, spec: GemSpec): GemQuery {
  const num = (k: string) => {
    const n = Number(p.get(k))
    return n > 0 ? n : undefined
  }
  const q: GemQuery = {
    buy: (p.get('buy') ?? '') as GemQuery['buy'],
    sort: p.get('sort') ?? '',
    page: num('page'),
    minPrice: num('minPrice'),
    maxPrice: num('maxPrice'),
    detailed: p.get('detailed') === '1',
    choices: {},
    ranges: {},
  }
  for (const f of spec.fields) {
    if (f.kind === 'choice') {
      const vs = p.getAll(f.key)
      if (vs.length) q.choices[f.key] = vs
    } else {
      const r = { min: num(`${f.key}_min`), max: num(`${f.key}_max`) }
      if (r.min || r.max) q.ranges[f.key] = r
    }
  }
  return q
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

// A site searches can watch ("ss.com", "rrr.lt") and what it offers.
export type Source = { id: string; categories: Category[] }

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
  source: string
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
  sources: () => call<Source[]>('GET', '/api/sources'),
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

  gemSources: () => call<GemSource[]>('GET', '/api/gems/sources'),
  gemScan: (source: string, category: string) =>
    call<GemScan | null>('GET', `/api/gems/scan?${target(source, category)}`),
  requestGemScan: (source: string, category: string, mode: GemScanMode) =>
    call<GemScan>(
      'POST',
      `/api/gems/scan?${target(source, category)}&mode=${mode}`,
    ),
  gems: (source: string, category: string, q: GemQuery) =>
    call<GemPage>(
      'GET',
      // lang: the verdicts in the reader's language.
      `/api/gems?${target(source, category)}&${gemParams(q)}&lang=${locale.value}`,
    ),

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
  live?: boolean // read from the site just now, not from collected ads
  scanned: number
  inScope: number
  matched: number
  unsure: number
  unsureKeys: string[]
  since?: string
  ads: PreviewAd[]
}

// Asks the site for its newest ads now (a button press; rate-limited).
export const previewLive = (input: {
  source: string
  category: string
  filters: Filters
}) => call<Preview>('POST', '/api/searches/preview/live', input)

export const previewSearch = (
  input: { source: string; category: string; filters: Filters },
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
