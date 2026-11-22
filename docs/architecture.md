# Architecture

One Go module, five services (`cmd/*`), Kafka between them, one Postgres.
Each `internal/` slice owns its tables; slices share only `internal/events`.
Each table has **one writing service**, which alone migrates it; the others
read it and send what to write as events.
Websites plug in as **source adapters** (`internal/sources` interface, one
package per site under `internal/scraping/`). Nothing else changes per site.

## Services

```mermaid
flowchart LR
    sites[(Websites)]
    chat([Chat apps])

    scraper

    subgraph bus["Kafka · event bus"]
        direction TB
        t1[/"listings.discovered"/]
        t2[/"listings.new"/]
        t3[/"notifications.requested"/]
        t4[/"listings.details"/]
        t5[/"gems.scan_requested"/]
    end

    ingest
    matcher
    notifier
    api

    sites -->|list pages| scraper
    scraper ==>|publish| t1
    t1 ==>|consume| ingest
    ingest ==>|publish| t2
    t2 ==>|consume| matcher
    matcher ==>|publish| t3
    api ==>|price alerts| t3
    t3 ==>|consume| notifier
    notifier --> chat
    matcher -.->|ad pages| sites
    matcher & api & scraper ==>|ad pages read| t4
    t4 ==>|consume| ingest
    api ==>|scan request| t5
    t5 ==>|consume| scraper

    classDef topic fill:#eef5fd,stroke:#0f6cbd,color:#0c5ba1
    class t1,t2,t3,t4,t5 topic
    style bus fill:#f6f7f9,stroke:#0f6cbd,stroke-width:2px
```

Around the bus: everything that talks to people, and the shared database.

```mermaid
flowchart LR
    user([Browser]) <--> api[api: dashboard + bot]
    chat([Chat apps]) <--> api
    api -.->|ad pages, shop pages| sites[(Websites)]
    pg[(Postgres)]
    api --- pg
    ingest & matcher & notifier & scraper --- pg
```

| Service | Job | Writes |
|---|---|---|
| scraper | Polls the feeds searches need, through each site's adapter; publishes every ad seen; runs gem scans | gems |
| ingest | Stores ads and what ad pages showed; outboxes the new ones (reposts marked) | listings |
| matcher | Matches new ads to searches; reads ad pages when needed; rates car alerts | matching |
| notifier | Delivers alerts, idempotent, with burst digests | notifications |
| api | Dashboard and JSON API, chat bot (`/watch`, `/searches`, `/dashboard`), price watches | subscriptions |

A reader waits at start until the owner has migrated the schema it was built
for (`AwaitSchema`). `admin` (migrate, purge) acts for every owner.

## A new ad, end to end

```mermaid
sequenceDiagram
    participant S as scraper
    participant I as ingest
    participant M as matcher
    participant N as notifier
    participant DB as Postgres

    S->>I: listings.discovered
    I->>DB: upsert listing + outbox row (one transaction)
    I->>M: listings.new (relayed from outbox)
    M->>DB: enabled searches
    opt a filter needs the ad page
        M->>M: read ad page once, shared by all searches
        M->>DB: save ad-page details
    end
    M->>N: notifications.requested (keyed by user)
    M->>DB: record matches
    N->>DB: dedupe by key
    N-->>N: send to chat
```

## Slices and their tables

One Postgres database, one schema (`public`). Each slice owns its tables and
migrates them itself (`<slice>_migrations`); no slice queries another's tables,
except the admin overview, a read-only report across all of them. No foreign
keys cross slices.

```mermaid
flowchart TB
    events[events: Kafka payloads]
    listings[listings] --- t1[(listings, listings_outbox, feed_state)]
    subs[subscriptions] --- t2[(users, accounts, channels, searches, sessions, login_tokens, settings, watch_targets, watches, watch_prices, watch_alerts)]
    match[matching] --- t5[(matches)]
    notif[notifications] --- t3[(deliveries, digest_queue)]
    gemsl[gems + categories] --- t4[(gem_scans, gems)]
    scraping[sources + scraping: one adapter per site]

    listings & subs & match & notif & gemsl & scraping -.-> events
```

## Adding a website

```mermaid
flowchart LR
    adapter[New adapter<br/>internal/scraping/site] -- implements --> src[sources.Source<br/>FetchFeed · PlanFeeds · InScope]
    adapter -. optional .-> cat[sources.Catalog<br/>search form fields]
    adapter -. optional .-> enr[sources.Enricher<br/>ad page details]
    adapter -. optional .-> car[sources.CarAds<br/>car verdicts]
    src --> reg[app.KnownSources<br/>register once]
```

Two adapters exist: `scraping/ss` (HTML feed pages, ad pages as `Enricher`, cars as `CarAds`) and `scraping/rrr`
(rrr.lt's GraphQL API, one query per search). The dashboard's editor shows a tab per registered `Catalog` (`GET /api/sources`).

## Price watches

```mermaid
sequenceDiagram
    participant U as Browser
    participant A as api
    participant S as api (watch loop)
    participant DB as Postgres
    participant N as notifier

    U->>A: inspect URL
    A-->>U: page copy in a sandboxed iframe + suggestions
    U->>A: picked selector (read back: "€1,299")
    U->>A: save watch (rule, interval, channel)
    A->>DB: target (shared) + watch, first price = silent baseline
    loop every interval, one read per page for all watchers
        S->>S: read page, parse price
        S->>DB: price row + watch state + alerts (one transaction)
    end
    S->>N: notifications.requested (from the alert outbox)
```

## Guarantees

- **No lost ads:** a listing and its outbox row commit together; Kafka keeps offsets per service.
- **Outages delay, never dead-letter:** a record failing because Postgres or Kafka is down is retried until they are back.
- **No duplicate alerts:** the notifier dedupes by `search:source:ad`.
- **Polite scraping:** searches share feeds, requests are spaced, failures back off.
- **Details are kept:** ad-page facts live in `listings.details`, which scrapes don't overwrite.
