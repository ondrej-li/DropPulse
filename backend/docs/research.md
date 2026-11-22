# Ticket availability backend — research

Status: **research only**, no implementation. Written 2026-11-22 against the
scaffolding in [`../`](../).

## What the backend has to do

- Users specify a **site**, a **venue** (optional) and one or more **events**.
- The backend **resolves and registers** those events, storing them in Postgres.
- It **monitors** each registered event on a schedule and records availability
  over time.
- Every site is a **plugin**: sites differ in authentication, endpoints, HTML,
  throttling rules and whether an API or a rendered page is the only way in.
- Some sites need **arbitrary code**, so an embedded scripting runtime is a
  first-class requirement.
- Users, authentication and notifications are deliberately out of scope for
  now, but the schema must not make them hard to add later.

## Recommendation at a glance

| Area | Recommendation |
|---|---|
| Storage | Postgres, `pgx/v5` + `sqlc`, migrations with `goose` |
| Domain | `sites → venues → events → monitors → checks`, config in layered JSONB |
| Plugins | In-process Go `Plugin` interface, three flavours: native, declarative, scripted |
| Scripting | Embedded JavaScript via [`sobek`](https://github.com/grafana/sobek) (pure Go), scripts compiled into the binary, narrow host API |
| Untrusted scripts | Not with sobek — use WASM (`wazero` / `Extism`) if users ever supply scripts |
| Scheduling | DB-driven due rows with `FOR UPDATE SKIP LOCKED` + leases; no job-queue dependency to start |
| Throttling | Per-site token bucket + concurrency cap, conservative defaults, exponential backoff |
| HTTP | One shared fetch client: allowlist, size/time caps, conditional requests, `robots.txt` |
| Policy | Official APIs first; never bypass CAPTCHA, anti-bot or queues |

The two decisions that need the owner's input are at the end: whether scripts
are trusted (written by us) or untrusted (supplied by users), and which sites
come first.

## 1. Policy constraints shape the design

This is a monitoring tool, not a ticket-buying bot. That distinction matters
technically, not just ethically: plugins must be able to say "blocked, backing
off" instead of escalating.

- Prefer an **official or public API** wherever one exists. Scraping is the
  fallback, not the default.
- Respect `robots.txt` and the site's terms. Honour `Retry-After`. Use a
  conservative default rate (for example one request every 5 seconds per site)
  and identify the service in the `User-Agent` with a contact URL.
- **Never** work around CAPTCHAs, bot detection, queue systems, login walls or
  access controls. A challenge page is a terminal `blocked` result, surfaced to
  the user; the monitor backs off and eventually pauses.
- No proxy rotation or browser-fingerprint spoofing intended to evade
  detection. That is out of scope for this project.
- Do not buy, share or automate accounts in violation of a site's terms.

The plugin API therefore has an explicit `blocked` outcome and the scheduler has
policy-aware backoff. If a site cannot be accessed legitimately, it is
unsupported — and that is a valid product answer.

## 2. Domain model (Postgres)

### Entities

| Table | Purpose | Notes |
|---|---|---|
| `users` | Minimal owner record | `id`, `email`; auth added later |
| `sites` | An integration instance | `kind` names the plugin; `config` holds connection defaults |
| `venues` | Site-scoped venue | `external_id` is the site's identifier; venue is optional for events |
| `events` | Site-scoped event | `(site_id, external_id)` natural key; shared by all users |
| `monitors` | A user watching an event | Per-event config overrides, interval, next run, failures |
| `checks` | Append-only history | One row per check; availability summary + offers JSONB |

Design choices:

- **External identity is `(site_id, external_id)`.** Events discovered again
  from a URL or search are upserted, not duplicated. Internal IDs are UUIDs so
  they can be exposed by the API without leaking volume.
- **Config is layered**: plugin defaults < `sites.config` < `monitors.config`.
  Only overrides are stored, so changing a site default affects every monitor
  that did not override it. The effective config is validated before use.
- **Checks are append-only history.** The current state is the latest check;
  alerts are computed from transitions, not stored as a mutable "available"
  flag. Raw HTML/JSON is not stored by default — it is large and sensitive. If
  debugging needs it, add a separate `check_payloads` table with compression
  and a short retention window.
- **Offers stay JSONB** initially. Ticket shape varies wildly per site; a
  normalized `offers` table would be premature. `min_price`, `max_price`,
  `currency` and an availability enum are promoted to columns for querying and
  alerting. Revisit if offer-level analytics becomes a requirement.
- **Retention**: schedule a periodic delete (or monthly partitioning) for
  `checks` older than N days/months. Not needed for the first version, but the
  schema should not assume infinite retention.

### Schema sketch

```sql
create table sites (
    id          uuid primary key default gen_random_uuid(),
    kind        text not null,                    -- plugin identifier
    name        text not null,
    config      jsonb not null default '{}',
    enabled     boolean not null default true,
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now(),
    unique (kind, name)
);

create table venues (
    id          uuid primary key default gen_random_uuid(),
    site_id     uuid not null references sites (id) on delete cascade,
    external_id text not null,
    name        text not null,
    city        text,
    country     text,
    timezone    text,
    metadata    jsonb not null default '{}',
    unique (site_id, external_id)
);

create table events (
    id          uuid primary key default gen_random_uuid(),
    site_id     uuid not null references sites (id) on delete cascade,
    venue_id    uuid references venues (id) on delete set null,
    external_id text not null,
    name        text not null,
    starts_at   timestamptz,
    ends_at     timestamptz,
    url         text,
    status      text,                              -- site-reported: onsale, cancelled…
    metadata    jsonb not null default '{}',
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now(),
    unique (site_id, external_id)
);

-- Minimal for now; authentication and profiles are a later phase.
create table users (
    id         uuid primary key default gen_random_uuid(),
    email      text unique,
    created_at timestamptz not null default now()
);

create table monitors (
    id                   uuid primary key default gen_random_uuid(),
    user_id              uuid references users (id) on delete cascade,
    event_id             uuid not null references events (id) on delete cascade,
    config               jsonb not null default '{}',
    interval_seconds     integer not null default 300 check (interval_seconds >= 60),
    next_check_at        timestamptz not null default now(),
    paused               boolean not null default false,
    lease_until          timestamptz,
    lease_owner          text,
    last_checked_at      timestamptz,
    consecutive_failures integer not null default 0,
    created_at           timestamptz not null default now(),
    updated_at           timestamptz not null default now()
);

-- NULL user_id rows are anonymous and therefore distinct under a plain UNIQUE
-- constraint; the partial index enforces one monitor per owner per event.
create unique index monitors_owner_event_idx
    on monitors (user_id, event_id)
    where user_id is not null;

create index monitors_due_idx
    on monitors (next_check_at)
    where not paused;

create table checks (
    id            bigint generated always as identity primary key,
    monitor_id    uuid not null references monitors (id) on delete cascade,
    event_id      uuid not null references events (id) on delete cascade,
    checked_at    timestamptz not null default now(),
    outcome       text not null,         -- ok | error | blocked
    availability  text,                  -- available | few_left | sold_out | not_on_sale | unknown
    min_price     numeric(12, 2),
    max_price     numeric(12, 2),
    currency      char(3),
    offers        jsonb not null default '[]',
    error_class   text,                  -- retryable | permanent | blocked | misconfigured
    error         text,
    duration_ms   integer,
    plugin        text not null,
    plugin_version text,
    unique (monitor_id, checked_at)
);

create index checks_monitor_time_idx
    on checks (monitor_id, checked_at desc);
```

`unique (monitor_id, checked_at)` is a cheap idempotency guard if a check is
accidentally retried within the same instant; it is not a substitute for
idempotent writes. Checks are otherwise insert-only, so retries are safe.

### Configuration shape

A monitor's config is a merge of plugin defaults, site config and monitor
overrides:

```json
{
  "interval_seconds": 300,
  "filters": { "quantity": 2, "max_price": 150, "currency": "GBP" },
  "http": {
    "headers": { "Accept-Language": "en-GB" },
    "timeout_seconds": 15
  },
  "plugin": {
    "performance_id": "12345",
    "include_resale": false
  }
}
```

Site config holds connection-level defaults: base URL, auth reference,
rate limits, minimum interval, default headers. Monitor config holds event-level
choices: filters, price ceiling, extra selectors. Each plugin publishes a JSON
Schema for both levels; the API rejects invalid config at write time and the
scheduler re-validates the effective config before every check (protects
against old rows after a plugin upgrade).

Secrets are **not** stored in `config`. Site config references environment
variable names (for example `"api_key_env": "TICKETMASTER_API_KEY"`) and the
host resolves them at call time. If DB-backed secrets become necessary, they
need application-level encryption with a key from the environment, not plain
JSONB.

## 3. Plugin model

### Contract

```go
// Plugin is the site integration contract. One implementation per site kind.
type Plugin interface {
    Kind() string
    Version() string
    Schemas() Schemas // JSON Schemas for site config and monitor config

    // Resolve turns a URL, an external ID or a search query into candidates.
    // Plugins that cannot search return ErrUnsupported for search inputs.
    Resolve(ctx context.Context, in ResolveInput, cfg Config) ([]Candidate, error)

    // Check returns the current availability for one event.
    Check(ctx context.Context, ev Event, cfg Config) (Availability, error)
}

type Availability struct {
    Status   Status   // available, few_left, sold_out, not_on_sale, unknown
    MinPrice *Money
    MaxPrice *Money
    Offers   []Offer
    Blocked  bool
    RetryAfter time.Duration
}
```

Plugins do not construct HTTP clients, open files, read the environment or log
to the global logger. The host injects the shared fetch client, clock, logger
and resolved secrets. That keeps throttling, SSRF protection, redaction and
timeouts in one place instead of re-implemented per plugin.

### Three flavours

1. **Native Go plugins** — compiled into the binary, registered explicitly in
   `cmd/server`. Best for official APIs where types and tests are worth it
   (Ticketmaster Inventory Status, SeatGeek, Eventbrite).
2. **Declarative plugins** — no code: a site config declares request URLs,
   method, headers, response type (HTML/JSON) and extractors (CSS selectors or
   JSON pointers), mapped to the common `Availability` shape. This covers many
   simple static sites and shrinks the set that needs scripts. Add it after the
   native contract is stable; it is, in effect, a data-driven plugin.
3. **Scripted plugins** — a JavaScript module implementing `resolve` and
   `check`, with host functions for HTTP and HTML. Used when a site needs real
   branching, pagination or bespoke parsing.

The registry maps `sites.kind` to one implementation. Scripts live in
`backend/plugins/scripts/<site>/plugin.js` plus `schema.json`, embedded with
`go:embed`. Treat them as source code: reviewed in PRs, versioned, tested
against fixtures. **Do not accept script uploads through the API in v1** — that
changes the threat model (see §4).

Proposed package layout once implementation starts:

```
backend/
  cmd/server/          # wiring only
  internal/domain/     # types shared by plugins, store, API
  internal/store/      # pgx + sqlc queries
  internal/plugin/     # contract, registry, conformance tests
  internal/plugin/builtin/
  internal/plugin/script/
  internal/fetch/      # shared HTTP client, robots.txt, rate limits
  internal/scheduler/
  internal/httpapi/
  db/migrations/
  db/queries/
  plugins/scripts/
```

## 4. Embedded scripting

### Engine comparison (as of November 2026)

| Engine | Language | Isolation | Resource limits | Fit |
|---|---|---|---|---|
| [`sobek`](https://github.com/grafana/sobek) (goja fork) | JavaScript ES5.1 + most ES6 | In-process; no ambient I/O | `Interrupt` between statements; no heap cap; host-driven timeout | **Recommended for trusted scripts** |
| [`goja`](https://github.com/dop251/goja) | JavaScript | Same | Same | Fine, but Sobek is the more active fork (k6 uses it) |
| [`gopher-lua`](https://github.com/yuin/gopher-lua) | Lua 5.1 | In-process; `SkipOpenLibs` | Context cancellation; no hard heap cap | Viable; smaller ecosystem for scraping helpers |
| [`starlark-go`](https://github.com/google/starlark-go) | Python-like, deterministic | Safe by construction; no I/O unless exposed | No `while` loops; host controls builtins | Good config DSL, awkward for page parsing |
| [`yaegi`](https://github.com/traefik/yaegi) | Go | Interpreter, not a security boundary | Context cancellation; slow | Only if Go syntax is a hard requirement |
| [`Extism`](https://extism.org/) on [`wazero`](https://wazero.io/) | Any → WASM | Strong: capability-based host imports | Memory pages, timeouts, compiled-plugin reuse | **Recommended if scripts become untrusted** |
| [`hashicorp/go-plugin`](https://github.com/hashicorp/go-plugin) | Any (gRPC/net-rpc) | Process isolation | OS-level | Good for heavy/browser plugins, overkill for scripts |
| stdlib `plugin` | Go | In-process `.so` | None | Rejected: exact toolchain match, Linux/macOS only, crash takes the host |

### Recommendation

Use **sobek** for scripted sites in v1, with these safeguards:

- A fresh runtime per check; no state leaks between events or users.
- `context` deadline (10 s default) wired to `Runtime.Interrupt`. Note that JS
  execution is only interrupted between statements, so a pathological builtin
  can overrun — acceptable for reviewed, in-repo scripts, not for untrusted
  code.
- No filesystem, process, environment or raw `net` access. The only way out is
  the host API below.
- Script source hash and plugin version recorded on every check.
- Offers capped (for example 500) and the returned object validated against the
  plugin's JSON Schema before it is persisted.

If the product ever allows users or third parties to upload scripts, move that
execution to **Extism/wazero**: memory limits, timeouts and capability-scoped
host functions, at the cost of a WASM toolchain per plugin. The Go-side plugin
contract does not change; only the runner does. This is the main open question.

### Script host API

```js
// plugins/scripts/example/plugin.js

function resolve(input, h) {
  // input: { url?, externalId?, query?, venue?, date? }
  const page = h.fetch({ url: input.url });
  return [mapEvent(page)];        // validated against the event schema
}

function check(event, config, h) {
  const res = h.fetch({
    url: event.url,
    headers: config.http?.headers,
  });
  if (res.status === 429) {
    return { blocked: true, retryAfterSeconds: 60 };
  }
  const $ = h.html(res.body);
  const soldOut = $(".sold-out").length > 0;
  return {
    status: soldOut ? "sold_out" : "available",
    offers: soldOut ? [] : [{
      section: $(".section").text(),
      price: Number($(".price").data("amount")),
      currency: "GBP",
    }],
  };
}
```

Host functions (all Go-side, all capped):

| Function | Behaviour |
|---|---|
| `h.fetch(req)` | Shared client: host allowlist, per-site rate limit, 10 s timeout, 5 MiB decompressed cap, no private-IP destinations, redirects re-checked |
| `h.html(body)` | Read-only `goquery` selection: `find`, `first`, `text`, `attr`, `data`, `each` |
| `h.json` | Plain JS `JSON`; no host needed |
| `h.log.info/warn/debug` | Redacted, rate-limited, size-capped |
| `h.secret(name)` | Only secrets declared by the plugin's site config; never logged |

No `setTimeout`/`setInterval`: a check is a short synchronous function. Pagination
is an explicit loop in the script with the host limiter applying backpressure.

## 5. Scheduling and execution

### Why not a job framework first

The workload is durable periodic state (`next_check_at`), not a stream of
transient tasks. Postgres can be the queue:

```sql
with due as (
    select id
    from monitors
    where not paused
      and next_check_at <= now()
      and (lease_until is null or lease_until < now())
    order by next_check_at
    limit $1
    for update skip locked
)
update monitors m
set lease_until = now() + interval '2 minutes',
    lease_owner = $2
from due
where m.id = due.id
returning m.*;
```

- The scheduler tick (every 10–15 s) claims a small batch and hands it to a
  bounded worker pool.
- A lease recovers in-flight rows after a crash; `SKIP LOCKED` lets more than
  one scheduler instance claim safely.
- On completion, the worker writes the `checks` row and updates
  `next_check_at`, clears the lease and resets/increments
  `consecutive_failures` in one transaction.
- This is roughly a few hundred lines and no new operational component.

When that stops being enough — many workers, complex retries, job visibility —
[`River`](https://riverqueue.com/) is the natural upgrade: Postgres-native,
transactional enqueue, unique jobs, periodic jobs, multiple queues and a UI,
with `pgx/v5` support. River is still pre-1.0 (v0.51.x as of Nov 2026), which is
a reason not to make it the first dependency. `asynq`/Redis adds infrastructure
this project does not otherwise need; Temporal is overkill.

### Throttling, fairness, backoff

- **Per-site token bucket** (`golang.org/x/time/rate`) keyed by `site_id`, plus
  a semaphore for `max_concurrency`. One slow or throttled site must not starve
  the others.
- Site config sets floor values: `min_check_interval_seconds`,
  `requests_per_second`, `burst`, `max_concurrency`. A monitor may be slower
  than the floor but never faster; the scheduler clamps.
- Add jitter (±10%) so monitors registered together do not synchronise.
- **Backoff**: success → normal interval. Retryable error (timeout, 5xx,
  connection reset) → exponential `min(base × 2^failures, max_backoff)`.
  429/`blocked` → `Retry-After` when present, minimum 15 minutes. Permanent
  error (event gone, parse failure after retries) → count; pause after 5 and
  surface the error to the user. Event in the past → pause automatically.
- **Multi-instance caveat**: `x/time/rate` is per process. With more than one
  checker, either shard sites to instances by hash, keep checking on a single
  writer, or move the token bucket into Postgres (atomic refill update). Do the
  simple thing while there is one process.

### Per-check flow

1. Scheduler claims a due monitor and records the lease.
2. Worker loads event + venue + effective config; validates the config.
3. Resolves the plugin by `sites.kind`; acquires the site limiter.
4. `plugin.Check(ctx, event, config)` with a whole-check timeout (30 s).
5. Normalizes the result, writes `checks`, updates monitor state and the next
   run.
6. Compares with the previous check and marks transitions (sold out → available,
   price drop) for the future notification layer.

## 6. HTTP fetching and parsing

One shared client, owned by the backend, used by native, declarative and
scripted plugins:

- `net/http` with sane timeouts; 5 MiB response cap enforced on the decompressed
  stream; limited redirects, each re-validated against the allowlist.
- Allowed hosts come from the site config (and the event URL's host must be
  covered). Block loopback, link-local, private and cloud-metadata addresses
  after DNS resolution to avoid SSRF, not just at URL-parse time.
- Conditional requests (`ETag`, `Last-Modified`) and a small cache reduce load
  and make 304 responses cheap. Respect `Cache-Control`.
- Static HTML parsing: [`goquery`](https://github.com/PuerkitoBio/goquery) v1.x.
  JSON APIs: `encoding/json`.
- `robots.txt` fetched and cached per host for scrape-based plugins; a
  disallowed path is a `blocked` outcome, not something to route around.
- JavaScript-rendered sites: first look for the underlying JSON endpoint (many
  sites ship one). If a browser is genuinely required, make it an explicitly
  configured, separate browser plugin (`chromedp`, `rod` or `playwright-go`).
  These cost hundreds of MB, start slowly and crash, so they should not share a
  process with the scheduler. Not part of v1.
- Error taxonomy is explicit: `retryable`, `permanent`, `blocked`,
  `misconfigured`. "Parse returned zero offers" is not the same as "sold out";
  both are recorded differently.

## 7. API surface (for the web and mobile clients)

Minimal v1, no auth yet; `user_id` is nullable so it can become required later.

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Liveness (exists) |
| GET | `/api/v1/plugins` | Registered site kinds + config schemas |
| POST | `/api/v1/events/resolve` | Site + URL/ID/query → candidate events |
| POST | `/api/v1/events` | Register a resolved event |
| GET | `/api/v1/events` | List/filter events |
| POST | `/api/v1/monitors` | Create a monitor for an event |
| GET | `/api/v1/monitors` | List a user's monitors |
| PATCH | `/api/v1/monitors/{id}` | Pause, change interval/config |
| DELETE | `/api/v1/monitors/{id}` | Stop monitoring |
| GET | `/api/v1/monitors/{id}/checks` | Availability history |

Auth, rate limiting and CORS for the public API come with the user work; do not
open CORS broadly in the meantime.

## 8. Testing

- **Plugin conformance suite**: every plugin runs against recorded fixtures
  through `httptest`, and its output must validate against the shared result
  schema. No live site is contacted in CI.
- **Golden files** for HTML/JSON parsing; sanitize fixtures before committing.
- **Fake clock** for scheduler/backoff tests; DB tests against a Postgres
  service container in CI (the repo already has CI; add a service).
- Script tests execute the embedded script against the same fixtures in sobek.
- Contract tests for `Resolve` ambiguity (zero, one, many candidates).

## 9. Prior art and the ticket-site landscape

- [`megafinz/ticket-monitor`](https://github.com/megafinz/ticket-monitor)
  (Deno/TypeScript): API + worker + MongoDB + Telegram alerts, watching a
  specific HTML element. It validates the "poll a page, alert on change" core,
  but has no plugin model, no per-site connection config and no schema
  validation — which is exactly the gap this project needs to fill.
- TicketRadar (Python) and similar projects are site-specific movie-ticket
  watchers. No mature, pure-Go, plugin-based availability monitor surfaced.
- API reality differs sharply per site, which is the argument for plugins:
  - **Ticketmaster Discovery API** is public but does not expose live
    inventory; real availability lives in the separately authorised
    **Inventory Status API** (`TICKETS_AVAILABLE`, `FEW_TICKETS_LEFT`, …).
  - **SeatGeek's** public API exposes event stats such as `listing_count` and
    `lowest_price` rather than individual listings.
  - **Eventbrite** exposes `on_sale_status` publicly; `quantity_sold` is
    restricted to event owners.
  - Many venues and smaller sites have no API at all and only HTML.

A plugin therefore needs to express: authentication style, endpoint/URL
templates, response format, availability semantics, throttle, and whether it
can resolve events by search.

## 10. Recommended stack

- Go 1.27, standard library HTTP server (as scaffolded).
- `github.com/jackc/pgx/v5` + `sqlc` for typed queries; `pressly/goose` for
  migrations. Keep SQL in `db/queries`, generated code out of hand-editing.
- `github.com/santhosh-tekuri/jsonschema/v6` for config schemas.
- `github.com/PuerkitoBio/goquery` for HTML.
- `github.com/grafana/sobek` for scripted plugins.
- `golang.org/x/time/rate` for per-site limits.
- `log/slog` for structured logs; Prometheus-style counters later.
- Optional, only when needed: River, chromedp/playwright-go, Extism/wazero.

## 11. Suggested phases

1. **Foundations** — migrations, config validation, plugin contract and
   registry, shared fetch client, one native plugin (API-based) and one generic
   HTML plugin, conformance tests.
2. **Registration** — resolve/register events, monitor CRUD, API endpoints.
3. **Monitoring** — due-row scheduler, leases, checks, backoff, per-site rate
   limits, history endpoint.
4. **Scripting** — sobek runtime, host API, two real site scripts, fixture
   tests.
5. **Notifications and users** — auth, alert channels, transition detection.
6. **Browser plugins** — only if a supported site genuinely requires them.

## 12. Open questions for the owner

1. **Trusted or untrusted scripts?** In-repo scripts (reviewed by us) allow
   sobek. User-supplied scripts require a WASM runner, which changes phase 4
   substantially.
2. **Which sites first, and do we have API access/keys?** Ticketmaster
   Inventory Status needs explicit authorisation; SeatGeek/Eventbrite have
   public credentials. A site list determines the first native plugins.
3. **Scale**: roughly how many events, monitors, and what check frequency?
   This decides whether the custom scheduler is enough and when retention or
   partitioning is needed.
4. **Deployment**: one backend process on the VPS, or multiple instances?
   Affects distributed rate limiting and scheduler design.
5. **Notifications**: which channels, and do alerts trigger on every check or
   only on transitions/price thresholds?
6. **Raw response retention**: keep HTML/JSON snapshots for debugging, or only
   the normalized result?
7. **User/auth scope**: anonymous monitors now and accounts later, or accounts
   from the start? The `user_id`-nullable schema supports either.

## Sources

- [River queue](https://riverqueue.com/) · [river on pkg.go.dev](https://pkg.go.dev/github.com/riverqueue/river) · [unique jobs](https://riverqueue.com/docs/unique-jobs)
- [SKIP LOCKED queue pattern (Prisma)](https://www.prisma.io/blog/you-dont-need-a-job-queue-postgres-already-has-skip-locked) · [Netdata: FOR UPDATE SKIP LOCKED](https://www.netdata.cloud/academy/update-skip-locked/) · [InfoQ: durable workflows on Postgres](https://www.infoq.com/articles/durable-workflows-postgres/)
- [pgx v5](https://pkg.go.dev/github.com/jackc/pgx/v5) · [sqlc](https://sqlc.dev/) · [goose](https://github.com/pressly/goose)
- [santhosh-tekuri/jsonschema v6](https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6)
- [sobek](https://github.com/grafana/sobek) · [goja](https://github.com/dop251/goja) · [gopher-lua](https://github.com/yuin/gopher-lua) · [starlark-go](https://github.com/google/starlark-go) · [yaegi](https://github.com/traefik/yaegi)
- [Extism host functions](https://extism.org/docs/concepts/host-functions/) · [Extism Go SDK](https://pkg.go.dev/github.com/extism/go-sdk) · [wazero](https://wazero.io/)
- [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin)
- [goquery](https://github.com/PuerkitoBio/goquery) · [colly](https://github.com/gocolly/colly) · [chromedp](https://github.com/chromedp/chromedp) · [playwright-go](https://github.com/mxschmitt/playwright-go)
- [golang.org/x/time/rate](https://pkg.go.dev/golang.org/x/time/rate)
- [Ticketmaster Inventory Status API](https://developer.ticketmaster.com/products-and-docs/apis/inventory-status/) · [Ticketmaster Partner Availability API](https://developer.ticketmaster.com/products-and-docs/apis/partner/availability/)
- [SeatGeek developer portal](https://developer.seatgeek.com/) · [Eventbrite platform API](https://www.eventbrite.com/platform/api)
- [megafinz/ticket-monitor](https://github.com/megafinz/ticket-monitor)
