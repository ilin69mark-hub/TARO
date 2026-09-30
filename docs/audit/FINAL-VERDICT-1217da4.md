# TARO — FORENSIC FULL-STACK AUDIT · FINAL VERDICT

**Target:** `/Users/j/Desktop/TARO` · **Commit:** `1217da4` ("security: close remediation and release blockers") · **Branch:** `main` · **Tree:** clean
**Date:** 2026-09-25 · **Mode:** read-only audit, no production code changed, no commits, no PR
**Template:** STAIR-PLATFORM FORENSIC FULL-STACK AUDIT, adapted — this project is a tarot product, so the "geometry/stairs" mandate (§45) is re-anchored to the mathematics that *is* critical here: the draw algorithm, entitlement/quota arithmetic, billing date math, fixed-window counters, and probability/reversal logic.
**Evidence base:** 8 audit passes from the same HEAD (persisted in Engram obs #1253–#1298) + a fresh re-verification pass executed today, including two findings that had been **lost with a crashed terminal session** and are now empirically proven.

---

## 1. REPOSITORY MAP

399 tracked files: 156 `.md`, 71 `.sql`, 59 `.go`, 37 `.ts`, 34 `.tsx`, 6 `.yml`, 5 `.sh`, 5 `.js`.

```
TARO/
├── api/                        Go 1.25 module `taro/api` (build, vet, race-clean)
│   ├── cmd/api/                public API  :8080  (30 distinct /v1 paths)
│   ├── cmd/admin/              admin API  :8082  (11 distinct /v1/admin paths)
│   ├── cmd/adminctl/           admin CLI (rotate-seasonal, no schema gate)
│   ├── internal/               19 packages: readings, ai, payments, entitlements,
│   │                           auth, me, referral, diary, push, ratelimit, admin,
│   │                           spreads, store, apierr, testutil, payments extras
│   └── migrations/             34 up + 34 down (golang-migrate)
├── web/                        Next.js 16.3.6 App Router (proxy.ts middleware)
│   ├── app/api/                22 route.ts handlers (Next-side BFF)
│   ├── app/                    spreads, history, diary, profile, share, reading…
│   ├── components/             22 components (CardArt, PaywallSheet, AgeGate…)
│   ├── public/sw.js            service worker (no push listener)
│   └── Dockerfile              2-stage, node:20-alpine, USER node
├── deploy/                     nginx.conf, nginx-tls.conf, migrate.sh, backup.sh,
│                               schema-ready.sh, recover-dirty.sh, admin-job.sh,
│                               admin-ui/ (635-line vanilla JS), docker-compose.{prod,tls}.yml
├── docker-compose.yml          dev topology: nginx, web, api-public, api-admin,
│                               admin-access, db, cache(redis 200mb allkeys-lru)
└── .github/workflows/          ci.yml, deploy.yml
```

**Data flow (verified):** `Browser → nginx(:80/:443) → [Next :3000 → Go :8080]` for `/api/*`, and `nginx → Go :8080` **directly** for `/v1/*` (nginx.conf:93-142). Next's `fwdHeaders()` is the only hop that rewrites `Origin`/`Referer`. Sessions live in Redis (`sess:*`), CSRF double-submit, entitlements cached under `ent:*`, AI responses cached 7 days.

---

## 2. AUDIT SCOPE

Covered: all 59 Go files, all 22 Next route handlers + 22 components, 34 migrations, 5 shell scripts, 3 nginx configs, 4 compose files, 2 CI workflows, 156 docs (docs-vs-code), 13 web/5 Go test-file quality, dependency freshness, secret scanning, supply chain, deploy safety, observability, concurrency, resilience, performance.

Not covered (see §27): live-stack behavioral claims, real Telegram Stars webhooks, real AI-provider latency, production cache saturation, k6/locust load.

---

## 3. EXECUTED CHECKS (today, real runs)

| # | Check | Result |
|---|-------|--------|
| 1 | `go build ./...` (go1.26.2) | **PASS** |
| 2 | `go vet ./...` | **PASS** |
| 3 | `go test -race -count=1 ./...` vs fresh PG16 + Redis | **14/14 packages PASS** (admin 84.8s, readings 8.3s) |
| 4 | `golang-migrate up` — all 34 migrations on virgin PG16 | **PASS**, `schema_migrations = 34, dirty = f`, 27 tables |
| 5 | `npx tsc --noEmit` | **PASS** |
| 6 | `eslint app components lib proxy.ts next.config.js vitest.config.mts` | **PASS** |
| 7 | `vitest run` | **PASS** 5 files / 21 tests |
| 8 | `npm audit --omit=dev` | **0 vulnerabilities**; lockfileVersion 3, 675 entries, no drift |
| 9 | `docker compose config --quiet` (dev) | **PASS** |
| 10 | `docker compose …prod…tls… config --quiet` | **FAIL** — `NEXT_PUBLIC_BASE_URL is required` (see F-32) |
| 11 | `gitleaks detect` (30 commits) | 21 `generic-api-key`, **all placeholders** (18 test files + 1 CI self-match) |
| 12 | **Worker drain probe** (real `ai.StartWorker`, backlog=10, 30s tick) | **2.00 readings/min** → F-06 |
| 13 | **Rate-limit eviction probe** (real `ratelimit.Limiter`, 200 MB allkeys-lru) | **40 requests in 2.1 s vs cap 30/60 s** → F-05 |
| 14 | **Redis-down probe** (prod go-redis options, fail-open path) | **1.714 s per request**, 20 calls = 34.3 s → F-42 |
| 15 | **Redis-down concurrency probe** (300 parallel) | all done in 962 ms, worst 961 ms, 4 dial attempts each → F-42 |
| 16 | prod required-var extraction vs `.env.example` | **6 of 15 missing** → F-32 |
| 17 | VAPID private-key literal search in tracked files | **none** → refutes a stale claim (§7, F-57) |
| 18 | Route inventory | 30 public `/v1/*`, 11 admin, 22 Next `route.ts` |

> All probes ran against throwaway containers (`audit_pg2` postgres:16, `audit_redis` redis:7 with the production `allkeys-lru`/200 MB policy) and scratch modules in `$TMPDIR`. The repo was not modified. The previously abandoned `r2-lru`/`r2-pg`/`audit_pg` containers were left untouched.

---

## 4. CRITICAL FINDINGS

### F-01 · Deploy is permanently blocked after the first backup
**Severity:** CRITICAL · **Status:** CONFIRMED · **Category:** CI/CD · **Location:** `.gitignore:1-10`, `deploy/backup.sh:6,68`, `deploy/README.md:139`, `.github/workflows/deploy.yml:84`

**Problem.** The deploy job refuses to run if the working tree has any untracked file. The documented nightly backup writes *inside the git worktree*, and `backups/` is not ignored. Once the cron has fired even once, every tagged release aborts at the preflight.

**Evidence.** `deploy.yml:84` → `test -z "$(git status --porcelain --untracked-files=all)"`. `backup.sh:6,68` → `BACKUP_DIR=${BACKUP_DIR:-/opt/taro/backups}`; the VPS checkout *is* `/opt/taro`. `deploy/README.md:139` documents `0 4 * * *`. `.gitignore` contains only `node_modules/ .next/ dist/ *.log .DS_Store .env .env.* !.env.example`. Earlier in this audit `git check-ignore -v --no-index backups/x` exited 1 (not ignored) and a scratch `git clone` + backup run reproduced the dirty tree.

**Reproduction.** `mkdir -p backups && touch backups/x && git status --porcelain --untracked-files=all` → non-empty → the deploy step returns 1.

**Impact.** Total loss of the release path. The project cannot ship any fix — including the fixes in this report — until this is repaired.

**Root cause.** Backup destination and the git worktree share a filesystem path; the ignore list and the deploy gate were written independently.

**Fix.** Add `backups/` (and `*.sql.gz`, `*.gpg`) to `.gitignore`, **and** move `BACKUP_DIR` outside the worktree (`/var/backups/taro`).

**Regression test.** CI step: create `backups/probe` in a scratch clone, assert the deploy preflight still passes; plus a docs assertion that `BACKUP_DIR` is not under the repo root.

---

### F-02 · `admin-access` netns desync kills the admin panel and all four cron jobs
**Severity:** CRITICAL · **Status:** CONFIRMED (live, earlier pass) · **Category:** Infrastructure · **Location:** `docker-compose.yml:130-153`, `deploy/admin-job.sh:60`

**Problem.** `admin-access` shares `network_mode: service:api-admin`. When `api-admin` is recreated (deploy, crash, `docker compose up -d`), `admin-access` keeps its **orphaned** network namespace and never re-attaches. There is no auto-heal.

**Evidence.** Live `ip -o addr` inside `admin-access` showed only `lo` and `:8082` with **no `eth0`**, while `api-admin` listened on `127.0.0.1:8081` inside a different netns — the signature of an orphaned namespace. Uptime skew (`api-admin` Up 2h vs `admin-access` Up 8h) proved they had diverged. `deploy/admin-job.sh:60` performs a health precheck that fails silently, so the 4 scheduled jobs (remind-expiring, push-evening, push-streak-risk, rotate-seasonal) never run.

**Reproduction.** `docker compose up -d --force-recreate api-admin` then `docker exec taro-admin-access-1 ip -o addr` → no `eth0`; admin UI returns 502 permanently until `admin-access` is recreated too.

**Impact.** Admin panel 502 + all revenue/retention cron jobs silently dead, with no alert. Rotations never apply, expiring-subject reminders never send.

**Root cause.** Namespace-sharing as an implicit dependency instead of an explicit network + DNS upstream.

**Fix.** Give both services a normal network, set `admin-access`'s upstream to `http://api-admin:8082`, and add `depends_on: api-admin: condition: service_healthy` (already present) — then recreate both together.

**Regression test.** A CI/compose test that recreates `api-admin` and asserts `/api/admin/health` through `admin-access` still returns 200.

---

## 5. HIGH FINDINGS

### F-03 · Web image/fetch cache is unwritable → silent 400s on every optimized image
**Severity:** HIGH · **Status:** CONFIRMED by repro · **Location:** `web/Dockerfile:17-25`, `web/app/**` (every `next/image`)

**Problem.** The build stage writes `/app/.next` as root; the runtime stage copies it and then drops to `USER node`. `.next/cache` is therefore `uid 0 mode 755` and the runtime user cannot create `cache/images`.

**Evidence.** Probed image at HEAD: `ls -ldn /app/.next/cache` → `0 0 … 755`. Unauthenticated `GET /_next/image?url=…` produced **210 `unhandledRejection: EACCES … mkdir '/app/.next/cache/images'` for 200 requests**; no rate limit on `location /` (nginx.conf:185). The live `taro-web-1` logged identical lines.

**Impact.** Every card image request burns CPU and log volume, returns the fallback, and floods logs. **Correction to an earlier claim:** the container *exit* was `OOMKilled=true` after ~4 h, not the EACCES — EACCES alone 400s the image request and the server survives. Severity: HIGH (degraded, unbounded log growth), not the P0 originally claimed.

**Fix.** `RUN mkdir -p .next/cache && chown -R node:node .next` in the build stage, or ship the `standalone` output and pre-create a writable cache dir.

**Regression test.** Container smoke test: `next start`, request `/_next/image`, assert zero `EACCES` in logs within 100 requests.

---

### F-04 · The card-art contract is unsatisfiable — 0 of 78 cards can render
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Frontend/Domain contract · **Location:** `web/components/CardArt.tsx:11-12`, `api/migrations/002_seed.up.sql:46-135`, `web/public/cards/`

**Problem.** The allowlist regex accepts `major-NN-name`, `minor-name-N` and `card-back`, but every seeded `image_key` is `cards/*.webp`. The `cards/` prefix is not in the allowlist, so the regex can never match a database value.

**Evidence.** `IMAGE_KEY_RE = /^(major-[0-9]{2}-[a-z-]+|minor-[a-z]+-[a-z0-9]+|card-back)\.webp$/`; all 78 seeded keys start with `cards/`. `web/public/cards/` contains only `README.md`. The allowlist is a deliberate traversal guard, so the mismatch fails **closed** (fallback shirt) — which is why this shipped unnoticed.

**Impact.** The entire visual product renders as a fallback. `public/cards/README.md` additionally claims "56 младших [x]" for an empty directory (docs-vs-code lie).

**Root cause.** Two teams picked two different art-key contracts; the guard was written to a contract that the seed never used.

**Fix.** Decide the contract, then make both sides match: either drop `cards/` from the seed (`major-00-fool.webp`) or add `cards\/` to the allowlist and commit the assets. The images must exist either way.

**Regression test.** Table test over all 78 seeded `image_key` values asserting `IMAGE_KEY_RE.test(key) === true`; plus an asset-existence test per key.

---

### F-05 · Rate-limit windows are reset by cache eviction — limit bypass proven
**Severity:** HIGH · **Status:** CONFIRMED (empirical, today) · **Category:** Security / Availability · **Location:** `api/internal/ratelimit/ratelimit.go:24-28,105-110`, `docker-compose.yml:33-35`

**Problem.** Rate limiting is a fixed window stored as a single Redis key (`rl:<prefix>:<ip|uid>`) created by `INCR` + `EXPIRE`. The cache that holds it is a **200 MB `allkeys-lru`** instance. Under memory pressure Redis evicts the counter, the window restarts at zero, and the limit is gone. Sessions (`sess:*`) and the 7-day AI response cache share the same instance.

**Evidence.** Probe against the real `ratelimit.Limiter` with the production policy:
```
STEP 1  burst of 20 (under cap):        allowed=20 limited=0   counter=20 ttl=1m0s
STEP 2  ballast → counter key evicted after 2 rounds (~160 MB), exists=0
STEP 3  burst of 20 MORE, same window:  allowed=20 limited=0   [t=2.1s]
RESULT: 40 requests inside one 60 s window, rule cap = 30  → INVARIANT VIOLATED
```
Redis reported `evicted_keys:15773`, `used_memory 199.94M / 200.00M`. (The first version of this probe printed "limit held" because its verdict logic compared the wrong number — corrected here.)

**Reproduction.** `lruprobe`/`lru2` against `redis-server --maxmemory 200mb --maxmemory-policy allkeys-lru`.

**Impact.** Unbounded brute force on `POST /v1/auth/*` (20/min/IP), API-cost abuse on `/v1/readings` (10/min/user), share-token enumeration, plus forced logouts (session eviction) and destruction of the paid AI cache — one memory-pressure event disables three subsystems at once.

**Root cause.** Security-critical, eviction-sensitive state co-located with bulk data under an LRU policy, with no key isolation and no `noeviction` guarantee for the counters.

**Fix.** Move `rl:*` and `sess:*` to a dedicated `maxmemory-policy noeviction` instance (or a separate Redis DB with its own budget and a much longer TTL floor); never let bulk caches share the limiter's instance. Long term, move the window to PostgreSQL.

**Regression test.** Property test: fill the cache until `maxmemory`, then assert ≤ `Max` requests succeed within `Window` seconds.

---

### F-06 · AI worker throughput is hard-capped at 2.00 readings/min
**Severity:** HIGH · **Status:** CONFIRMED (empirical, today) · **Category:** Performance / Domain · **Location:** `api/internal/ai/worker.go:20`, `api/cmd/api/main.go:90`

**Problem.** `workerBatchSize = 1` and the worker is a **single goroutine** driven by a 30-second ticker. Throughput is therefore 2 readings/min = **2 880/day**, independent of backlog size.

**Evidence.**
```
workerBatchSize = 1                       ai/worker.go:20
gw.StartWorker(workerCtx, pg, 30*time.Second)   cmd/api/main.go:90
--- real StartWorker, backlog 10 ---
t= 30s backlog=9  t= 60s backlog=8  t= 90s backlog=7  t=120s backlog=6
t=150s backlog=5  t=180s backlog=4  t=210s backlog=3  t=240s backlog=2
RESULT: drained 8/10 in 4.0 min => 2.00 readings/min sustained
```

**Impact.** A perfectly correct draw algorithm (verified statistically, §10) is irrelevant if it never runs. Any provider stall (F-11) or traffic burst grows the backlog without bound; the free-tier daily slot is already consumed (F-12), so users pay and wait. At 2 880/day the AI cache alone (7-day TTL) reaches 40–80 MB — the mechanism behind F-05's production trigger.

**Fix.** Configurable batch (`workerBatchSize` → env, default 10) **and** shorten the tick to 2–5 s, or convert to a `FOR UPDATE SKIP LOCKED` work queue with N workers. Target ≥ 60 readings/min with a 10× burst headroom.

**Regression test.** Insert 100 pending readings, assert the backlog drains in < 2 min and that no reading is processed twice.

---

### F-07 · Concurrent payment webhooks lose entitlement days
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Database / Money · **Location:** `api/internal/payments/payments.go:605-622`

**Problem.** `grantPaymentEntitlements` computes the new expiry with `SELECT … GREATEST(COALESCE(MAX(valid_until), now()), now()) + make_interval(days => $6) FROM subscriptions WHERE user_id=$1 AND status='active'` and inserts. The read is unlocked; there is no user-scoped lock and no unique index on `subscriptions` (`idx_sub_payment_source` is non-unique).

**Evidence.** Real handler, 2 concurrent webhooks: **11/12 runs both returned HTTP 200 `{"ok":true}` and the user received 30 days for two paid months**. Raw SQL pair with zero artificial delay: **40/40 lost updates**. A forced `pg_sleep(3)` trigger on `payments` UPDATE reproduced it deterministically.

**Root cause.** Read-modify-write on an aggregate with no serialization. The codebase already owns the fix pattern: `pg_advisory_xact_lock(hashtext(…))` at `readings.go:316-317`, `ai.go:581`, `auth.go:402`, `link.go:158`, and a correct uniqueness guard at `auth.go:590` (`trial_grants … ON CONFLICT DO NOTHING`).

**Fix.** `SELECT pg_advisory_xact_lock(hashtext('sub:'||$1))` as the first statement of the grant transaction, and add a **unique** index on `subscriptions(payment_id) WHERE payment_id IS NOT NULL`.

**Regression test.** Two concurrent `HandleWebhook` calls for the same user; assert `valid_until ≈ now()+60d` and exactly two subscription rows.

---

### F-08 · Webhook ↔ `DELETE /v1/me` deadlock (40P01) with a silent session purge
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Concurrency / Money · **Location:** `payments.go:154-164`, `me.go:78-87`, `me.go:47-61`

**Problem.** Opposite lock orders. The webhook takes `FOR UPDATE OF p` on the payment row and then requests a `KEY SHARE` on the user (via the subscriptions FK). `DELETE /v1/me` takes the user row lock and then cascades to `payments`.

**Evidence.** Real `40P01` raised in both directions; 30 deadlocks logged server-side. In 20/20 real-handler runs PostgreSQL always aborted the DELETE (it looks cheapest), producing `500 "Не удалось удалить данные"` on delete while the webhook succeeded. The *consequence chain* is real and dangerous: should the webhook ever be the victim, `loadWebhookPayment` returns `ErrNoRows` → `writeWebhookDuplicate` → **HTTP 200 with zero rows in `payment_webhook_events`** → money captured, entitlement never granted, silently acknowledged. Forcing the webhook to be the victim required inflating the delete cost by 8M row aggregates; 4 000 real cascading readings did not flip it. **Merge branch is unreachable** (`link.go:171-187` requires the loser's `tg_id IS NULL`, which forces the webhook into the `owner_unverified` branch at `payments.go:780-808` that never inserts subscriptions) — so the earlier "merge deadlock" claim is REFUTED.

**Additional confirmed impact:** `me.go:47-61` purges `sess/csrf/ent` keys **before** the failed transaction, so a 500 on delete still forces a full re-login.

**Fix.** Define one global lock order (users → payments) and make the webhook acquire the user lock *first*; or drop the FK-driven lock by inserting subscriptions without a user-row lock (deferred FK). Purge Redis keys **after** a successful commit.

**Regression Test.** Concurrent `DELETE /v1/me` + webhook on the same user, 50 iterations: assert no `40P01`, delete either succeeds or returns a clean 4xx, and the session survives a failed delete.

---

### F-09 · `payment_webhook_events` is written but never read
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Observability / Money · **Location:** `payments.go` (writes only), `me.go` (deletes), `api/migrations/033_*`, `deploy/schema-ready.sh`, `api/cmd/adminctl/main.go`

**Problem.** The audit table records `duplicate_charge` and other webhook mismatches. No code path ever reads it: there is no admin endpoint, no `adminctl` payment command, no alert, no reconciliation job.

**Evidence.** Grep for readers across the whole module returns zero; `schema-ready.sh` only checks structure. `go test ./...` passes with the table permanently empty of any consumer.

**Impact.** Captured duplicate charges are never detected, refunded or reported. Combined with F-08's `ErrNoRows → 200` path, a money-losing event is recorded in a table nobody opens.

**Fix.** `adminctl payments reconcile --since`, an admin `/v1/admin/payments/audit` view, and an alert when `duplicate_charge` rows appear.

**Regression test.** Insert a `duplicate_charge` row, assert the reconcile command reports it.

---

### F-10 · Crisis/safety moderation is trivially bypassable
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Security / Domain · **Location:** `api/internal/…/filter.go:10,16-18,21-29`

**Problem.** `ContainsStopWords` is a raw `strings.Contains` with no normalization. Proven bypasses: Cyrillic homoglyphs, zero-width space (`умр\u200bрёшь`), newline splitting, letter-spacing, Latin `a` (`сaмоубийство`), inflections (`умрёшь`, `Вы умрёте`, `Он умрёт`), and English (`You will die`, `Just kill yourself`). **16 of 21 hostile strings miss the filter.** Additionally `ContainsStopWords(SafeReplacement) == true` — the safe-replacement text itself contains "приговор", so the filter's own output trips its own predicate.

**Evidence.** Proven in the earlier pass and re-confirmed by test-the-tests; the assertion style is `assert.Equal(t, tt.want, ContainsStopWords(tt.in))` with no normalization step anywhere in the package.

**Impact.** The product's safety gate provides no real protection while the UI implies it does. For a consumer app that also stores user diary text about death and mental health, this is a product-liability defect, not just a bug.

**Fix.** Normalize before matching: `unicode/norm` NFKC, strip `\u200b-\u200d\ufeff`, fold `аё` homoglyphs, collapse whitespace, lowercase, and stem/lemmatize the stop list. Separate the "is this text a crisis message" predicate from the "is this text my own safe replacement" constant.

**Regression Test.** The 21-string hostile corpus as a golden table test, plus `ContainsStopWords(SafeReplacement) == false` as a permanent assertion.

---

### F-11 · An 8 s timeout aborts healthy generations and poisons the circuit breaker
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Resilience / Money · **Location:** `ai.go:27,114,742`

**Problem.** `RequestTimeout = 8s` is used both as `ResponseHeaderTimeout` **and** as the whole-attempt deadline, so it must cover TTFT + a full 900-token generation.

**Evidence.** Probe with a mock provider emitting a **healthy 12 s stream** (steady chunks, no errors): the attempt was aborted at 8 s and counted as a provider failure, after which the breaker opened and every subsequent request took the fallback path.

**Impact.** Any slow-but-healthy provider response is charged as a failure; the breaker opens on provider *latency*, not provider *failure*; the user is silently downgraded to the fallback text and (F-12) the entitlement is gone.

**Fix.** Split the budgets: header/TTFT timeout (e.g. 8 s) **and** an overall generation deadline (e.g. 60 s) as separate `http.Client` / context values. Exclude client-cancellation and deadline-exceeded from breaker-failure accounting.

**Regression Test.** Mock provider with a 12 s healthy stream must complete; mock provider with a 5xx must still open the breaker.

---

### F-12 · A provider stall consumes the entitlement and hides the reading
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Domain / Money · **Location:** `readings.go:459` (`AuthorizeReading`), `ai.go` worker transitions

**Problem.** The free daily slot (and the single-purchase receipt) is consumed **before** any provider call. A stall produces 3 attempts → `pending_fallback` → `failed`; `free_used_today` stays spent, and the UI treats a failed reading as "no reading".

**Evidence.** Confirmed causal chain at HEAD with a stalling mock provider.

**Impact.** The user is charged (or loses their daily slot) for a service that returned nothing, and cannot tell the difference between "no reading today" and "our provider hung".

**Fix.** Reserve the entitlement in a `reserved` state and release it on terminal failure; expose `failed_at`/`failure_reason` to the client and let the UI offer a free retry.

**Regression Test.** Stall the provider; assert `free_used_today` returns to its pre-request value and the reading row is `failed` with a reason.

---

### F-13 · 18 of 22 Next handlers drop the upstream `Set-Cookie`
**Severity:** HIGH · **Status:** CONFIRMED (measured today) · **Category:** Cross-layer contract · **Location:** `web/app/api/**/route.ts`, `api/internal/auth/session.go:157-159`, `ratelimit.go:120`

**Problem.** Most Next API routes build a fresh `NextResponse.json(...)` with only `Content-Type`, discarding every upstream header. Only `auth/anon`, `auth/telegram` and `me` use the pass-through helper. Measured today: **18 files construct a fresh response, 3 use pass-through.**

**Impact.** (a) The CSRF **self-heal** — the API re-issues a rotated CSRF cookie in `Set-Cookie` (session.go:157-159) — is dropped on 10 authenticated mutating routes, so a rotated token can never be recovered through the BFF. (b) `Retry-After` from the limiter is dropped, so clients see a bare 429 with no backoff hint.

**Fix.** One shared `relay()` helper used by every route that must forward `Set-Cookie`/`Retry-After`; add a test asserting both headers survive for each mutating route.

**Regression Test.** Table test over all 22 routes: call with a rotating session, assert the new `taro_csrf` cookie reaches the browser.

---

### F-14 · No `X-Frame-Options` in production, and two contradictory CSPs
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Security · **Location:** `deploy/nginx.conf:30-31,33-37`, `deploy/nginx-tls.conf:30-31`, `web/next.config.js:22`

**Problem.** nginx **hides** the upstream `X-Frame-Options` and `Content-Security-Policy` headers, then re-adds its own CSP — but its `add_header` list (lines 33-37) contains **no `X-Frame-Options` at all**. Net effect: zero clickjacking protection in production. The two CSPs also disagree: `next.config.js:22` says `frame-ancestors 'none'`, nginx says `frame-ancestors 'self' https://web.telegram.org`.

**Evidence.** Direct read of `nginx.conf`; Next's `headers()` are baked into `.next/routes-manifest.json`, so they *are* emitted by the app and then stripped.

**Impact.** Clickjacking on every page, including the paywall and the age gate (F-40). The Telegram WebApp allowlist in nginx is presumably deliberate — but it is undocumented and unenforced by any test.

**Fix.** `add_header X-Frame-Options "SAMEORIGIN" always` (or drop the `proxy_hide_header` for XFO), and reconcile the two `frame-ancestors` values in one place.

**Regression Test.** `curl -sI https://<host>/ | grep -qi x-frame-options` in CI against the built stack.

---

### F-15 · EOL toolchain and floating base images
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Supply chain · **Location:** `api/go.mod:3,20-23`, `.github/workflows/ci.yml:20,25,66,82`, `web/Dockerfile:5,17`, `docker-compose.yml:131,186`, `api/Dockerfile.public:2,9,11`, `api/Dockerfile.admin:4,12,14`

**Problem.** `go 1.25.0` (Go 1.27 shipped 2026-08-19; go1.25.14 was the last 1.25 release), `node:20-alpine` (20.20.2 final), `alpine:3.20` (3.20.10 final), `nginx:1.27-alpine` (1.27.5 final). CI pins `go-version: "1.25"`, so it can never detect EOL. 6 of 9 infra images use floating tags, so "roll back to the previous tag" does not reproduce the previous artifact.

**Evidence.** Zero-network proof via `docker image inspect --format '{{.Created}}'`: nginx:1.27-alpine Created 2025-04-16 == the 1.27.5 release date; alpine:3.20 == 3.20.10; node:20-alpine == 20.20.2; golang:1.25-alpine == go1.25.14. Go policy (go.dev/doc/devel/release): "supported until there are two newer major releases". `go mod tidy -diff` and `go mod verify` are clean — integrity is sound, only freshness is broken. `npm audit` = 0 today.

**Fix.** go→1.27, node→24 LTS, alpine→3.22+, nginx→1.29+; digest-pin the 6 floating bases; add a weekly Dependabot/`docker scout` job.

---

### F-16 · No rollback path and no pre-migration backup
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Deploy · **Location:** `.github/workflows/deploy.yml:75-105`, `deploy/README.md:69,139,150`, `deploy/migrate.sh`

**Problem.** `deploy.yml` runs `stop → migrate → recreate` with no snapshot and no rollback step; it never calls `deploy/backup.sh` (backups exist only as a host crontab in the README). `migrate.sh down` requires a TTY, so it cannot be used from CI.

**Evidence.** Read of `deploy.yml:94-105`; `migrate.sh` `2>&2` down-guards and interactive prompts. The nightly-backup trigger is `deploy/README.md:139` only.

**Impact.** A bad migration is a one-way door: recovery means SSH-ing into the VPS by hand during an incident.

**Fix.** Call `backup.sh` before `migrate.sh up`; capture the pre-migration image digests; add a `workflow_dispatch` rollback job; add `migrate.sh down --force` for non-interactive use.

---

### F-17 · CI cannot detect the defects in this report
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** CI/CD · **Location:** `.github/workflows/ci.yml`

**Problem.** No `govulncheck`, no Trivy/gitleaks, no coverage gate (`go test -race -count=1 ./...` with no `-cover`/`-coverpkg`/threshold), no `tsc` over `*.test.ts`, no `docker compose config` validation, no web image build, `admin-ui/app.js` (635 lines) checked only with `node --check`, and the secret scan is **one hard-coded literal** with two self-matching exclusions. The PII grep misses aliased/nested properties.

**Evidence.** Read of `ci.yml`; today's `gitleaks` run found 21 hits that the CI's single-literal rule would not; today's `compose …prod…tls… config --quiet` **fails** and CI never runs that command in prod mode.

**Impact.** A fully green pipeline coexists with 2 CRITICAL and 18 HIGH defects. The pipeline is not evidence of correctness.

---

### F-18 · The tests cannot detect the money-path defects (test-the-tests)
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Testing · **Location:** `payments/extra_e2e_test.go:28-29,108-118`, `integrity_e2e_test.go:717,761`, `cmd/admin/main.go:110`, `auth/handlers_e2e_test.go:77-81,88,152`, `ratelimit/ratelimit_test.go:22`, `readings_e2e_test.go:190,256`, `web/lib/api.test.ts:28-49`

**Problem.**
1. **Refund tests bypass authorization** — they mount `HandleRefund` bare, while production wires it behind `RequireAdmin` (`cmd/admin/main.go:110`). An authorization regression on a money endpoint is invisible.
2. **The refund-failure path is env-masked** — CI never sets `TG_BOT_TOKEN` (`ci.yml:58-61`), so the `!tgTokenReady()` early return at `payments.go:1189` always fires; with a real token the test *fails*, because a failed Telegram refund leaves the payment in `status='refunding'`.
3. **Tests contaminate the shared cache** — `handlers_e2e_test.go:77-81` `SCAN`+`DEL`s every `rl:*` key on the live Redis while packages run in parallel. This is the true source of the `rl:reg:unknown` key that an earlier pass misread as a production symptom.
4. **`go test -count=2` fails** — `ratelimit_test.go:22` hardcodes IP `9.9.9.9`, so the second run gets 429 where it wants 200.
5. **Coverage shape** — 285 status-code assertions, 18 `strings.Contains`, **0** `DeepEqual`/`cmp.Diff`, 8 subtests total. Real cross-package coverage is 66.3%. The limiter has 10 rules and 1 test; the `failClosed` 503 branch and the `byUser`/`taro_admin` branch are never exercised; 5 routes have no rule and there is no route-vs-rule invariant test. The SSE contract is untested on both sides. 0 fuzz targets.

**Root cause.** Tests were written to the current implementation (assert what it does) rather than to invariants (assert what must be true). Names like `TestE2ERefundPaths` create the illusion of coverage.

---

### F-19 · A panic kills the pending-payment expiry job permanently, silently
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Resilience / Observability · **Location:** `api/cmd/api/main.go:92-109`, `apierr/apierr.go:73`

**Problem.** `defer apierr.Recover()` is placed **outside** the 5-minute ticker loop, so a panic inside `py.ExpirePending` terminates that goroutine forever. `apierr.Recover()` is literally `_ = recover()` — no log, no metric, no restart. `healthz`/`readyz` keep returning 200.

**Evidence.** Read of `main.go:92-109`; `ai/worker.go:105-108` places the same `defer` correctly (inside the loop body) — the codebase contains both patterns.

**Impact.** Expired invoices stop being expired (revenue leakage) with **zero** signal. This is the textbook answer to "if prod breaks at 03:00, can you tell from telemetry?" — no.

**Fix.** Move the `defer` inside the loop body, make `Recover()` log with stack + request context, and expose a `last_tick_unix` gauge.

---

### F-20 · There is effectively no observability
**Severity:** HIGH · **Status:** CONFIRMED · **Category:** Observability · **Location:** whole `api/` tree

**Problem.** The entire Go API contains **10** `log.Printf` calls, all of them server lifecycle in `cmd/*/main.go`. No `slog`, no structured request logs, no request/correlation IDs, no metrics endpoint, no tracing, no error tracking. Sensitive values (share tokens, diary text) are handled with the same absence of discipline that produced F-14.

**Impact.** Every incident in this report would have been found by a customer, not by a dashboard. Detection time for the deadlock (F-08), the breaker storm (F-11) or the dead cron jobs (F-02) is unbounded.

**Fix.** `log/slog` JSON to stdout, a request-ID middleware, `/metrics` (Prometheus) with the 10 metrics you would actually alert on, and error tracking for 5xx.

---

## 6. MEDIUM FINDINGS

| ID | Finding | Evidence | Fix |
|----|---------|----------|-----|
| **F-21** | `fwdHeaders()` does not forward `X-Real-IP`; `byUser:false` rules (`/v1/auth/` 20/min, `/v1/spreads` 60/min, `/v1/share` 30/min) collapse into one bucket keyed on the web container IP. **Correction:** the live `rl:reg:unknown` key was a *test* artifact (F-18.3), so the original "global anon-login P0" is downgraded to MEDIUM — the code gap is real, the P0 framing was not. | `web/lib/proxy.ts:41-57`, `auth.go:348-351` | forward `X-Real-IP`/`X-Forwarded-For`; add a route test asserting distinct IPs get distinct buckets |
| **F-22** | SSE branch returns 200 with only `{"pending":true}` and no `done` frame; `web/lib/api.ts:73-97` never requires one → resolves as **success** with `reading_id:""`, spread page renders nothing though the quota was spent. The JSON branch returns 202 for the same state (asymmetric contract). | `readings.go:846-852` vs `:799-814`; `api.ts:79-97` | require a terminal frame client-side; make the SSE branch return 202 + `reading_id` |
| **F-23** | Non-2xx `<500` provider bodies (402/401/403/404/422/429) are read up to 1024 raw bytes and written verbatim into `ai_logs.error`, surfaced in the admin UI. Provider error text can echo prompt fragments. | `ai.go:471-473` → `ai.go:354` | store the status code + a redacted, length-capped excerpt |
| **F-24** | Referral 30-day/month cap is check-then-act: read `referral_bonus_month`, compare, then grant. Two concurrent completions → month = 33. | `referral.go:179-186`; proven 10/10 with the real handler | move the cap into the same statement as the counter upsert, or lock the entitlements row |
| **F-25** | On account merge, the dedup can delete the subscription row that carries `payment_id`; a successful Telegram refund then revokes nothing. Entitlement days are provably **not** lost (60 d before and after) because a revoked row always has the smaller `valid_until` and is always the deleted one. | `link.go:255-259` + `payments.go:1131-1144` | never delete a row referenced by a payment; make refund revoke fall back to the keeper row |
| **F-26** | Pending-invoice retries create **multiple** Telegram invoice links. The web client reuses one idempotency key, so this is API-only abuse (limit 10/60 s per user). | `payments.go:957-964` | reuse the existing `telegram_invoice_id` until expiry |
| **F-27** | `recover-dirty.sh:156` runs `psql -f` without `--single-transaction`; a failure mid-file leaves half-applied migrations. (`migrate.sh up` uses golang-migrate, which *is* atomic per file and sets `dirty=true` correctly.) | proven with a disposable PG | add `--single-transaction` (or `-1`) |
| **F-28** | `SCHEMA_READINESS_MODE=deploy` verify fails on any DB with an empty `admin_accounts`; the seed admin is not counted. `adminctl` has no schema-version gate although its rotate path needs migration 030. | `schema-ready.sh:606-620`, `adminctl/main.go:145-160` | count the seed admin; add a version gate to `adminctl` |
| **F-29** | `web/public/sw.js` has **no `push` listener and no `showNotification`** while the UI states "Пуши включены". Push unsubscribe and share revoke exist in Go but have no Next route and no UI. | `sw.js`, `PushOptIn.tsx` | implement the receive path or remove the claim; add the missing routes |
| **F-30** | Share previews are dead: no `metadataBase` and a relative `og:image` → Next 16.3.6 resolves it to `http://localhost:3000` (verified in `next/dist/lib/metadata/resolvers/*`). nginx access logs retain share tokens; the TLS redirect trusts `$host`. | `app/layout.tsx`, `next/dist/…/resolve-opengraph.js:86-102` | set `metadataBase`; redact `share/*` in `log_format`; use `$server_name` for the redirect |
| **F-31** | `next build` throws without `NEXT_PUBLIC_BASE_URL` (`sitemap.ts:6-8`) and the Dockerfile `ARG` default is `""` → a plain `docker build` fails; the compose default `http://localhost:3000` defeats the fail-closed guard and bakes a localhost sitemap. | `web/Dockerfile:5,14`, `docker-compose.yml:160` | make the ARG required in both |
| **F-32** | **Prod `compose config` fails.** `deploy/docker-compose.prod.yml` requires 15 variables; **6 are missing from `.env.example`**: `ADMIN_API_TOKEN`, `DATABASE_URL`, `OPENROUTER_API_KEY`, `REDIS_ENC_KEY`, `REDIS_PASSWORD`, `TLS_CERT_DIR`. `deploy.yml:75-84` preflights only `.env` existence/mode plus `BACKUP_GPG_KEY` and `TLS_CERT_DIR`, then runs `compose config --quiet` → a raw interpolation error mid-deploy instead of a clear preflight. | verified today: `docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml -f deploy/docker-compose.tls.yml config --quiet` → `NEXT_PUBLIC_BASE_URL is required` | document + template the 15 required vars; validate all of them in the deploy preflight; run `compose config` in CI for prod+tls |
| **F-33** | `/api/diary/export` buffers the entire export through `arrayBuffer` in a 1 GiB web container (guard allows 5 000 entries × 10 k runes). | `web/app/api/diary/export/route.ts` | stream the response |
| **F-34** | Analytics events `paySuccess`, `trialStart`, `deleteMe` are never emitted, although `08-analytics-spec.md` and `04-kpi.md` build the Monday KPI funnel on them; `paywall_show` always sends `plan:"unknown"`. No server-side fallback. | `web/lib/analytics.ts:46-47`, `api.ts:22,68` | emit the events or delete the funnel from the docs |
| **F-35** | Time-base split: the referral month key (`time.Now().Format("2006-01")`) and the seasonal rotation use server-local time while everything else uses MSK. On a UTC server the month cap flips a day early and the rotation window shifts. | `referral.go:178`, `admin.go` rotate | one `taroNow()` helper in MSK, used by all window keys |
| **F-36** | `ab.price_month` validator accepts a float (`split`) but `VariantFor` unmarshals into an `int` → a decimal value silently disables A/B. | `payments.go:352` vs `VariantFor` | int-only validation, or parse both |
| **F-37** | 5 `app_config` knobs are admin-editable and validated but **never read**: `referral.enabled/bonus_days/monthly_cap`, `copy.paywall_{title,desc,cta}`, `history.free_limit`. There is no referral kill switch. | `admin.go` config vs readers | wire them or delete them from the admin UI |
| **F-38** | A second, weaker quota implementation survives as dead code: `entitlements.Service.Check`, `pgConsume*`/`consume`/`cacheQuota`, `GrantBonusDays` (0 callers). The live rule is `AuthorizeReading` + `reading_authorization_receipts`. `ent:*` Redis keys are write-only although the architecture doc names Redis as the limit enforcer. | `entitlements.go:763-779` | delete, or make the single source and update the doc |
| **F-39** | `retryAuth` (`auth.ts:127`) is exported and never called → any `ensureAuth()` failure is permanent until reload; after `DELETE /api/me` the deleted user's JWT keeps being sent. | `web/lib/auth.ts:127`, `profile/page.tsx:117-138` | call it on 401, reset `authPromise` after delete |
| **F-40** | `AgeGate` and `Onboarding` are both fixed `inset-0 z-30 aria-modal` siblings in `layout.tsx:42-43` → Onboarding paints over AgeGate and the 18+ notice is unreachable on first visit. | `Legal.tsx:30`, `Onboarding.tsx:69` | sequence them: no onboarding before age acceptance |
| **F-41** | Neither HTTP server sets timeouts: `http.Server{Addr, Handler}` at `cmd/api/main.go:183` and `cmd/admin/main.go:123` — no `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `MaxHeaderBytes`. Severity is MEDIUM not HIGH because `docker-compose.prod.yml:5,12` uses `ports: !reset []` (nothing host-published) — but the **dev/staging** config publishes ports (`docker-compose.yml:14,41`) and SSE routes legitimately need no write timeout, so the other routes need one. | direct read | set `ReadHeaderTimeout`/`IdleTimeout` everywhere; a write timeout on non-SSE routes only |
| **F-42** | With Redis unreachable, every **fail-open** request costs **1.714 s** (prod options: only `DialTimeout` is set, so go-redis defaults apply — `MaxRetries=3`, `ReadTimeout=5s`, `PoolTimeout=6s`); 20 sequential calls = 34.3 s. 300 concurrent fail-open requests: all complete in 962 ms, worst single request 961 ms, 4 dial attempts each. `failClosed` rules return 503 after the same 1.7 s stall instead of failing fast. | probes run today against `store.go:26-30` options | set `ReadTimeout`/`PoolTimeout`/`MaxRetries` explicitly; give fail-open paths a ~50 ms budget with a single attempt; make `failClosed` fail fast |
| **F-43** | The 7-day AI response cache (`ai.go:665`, `CacheTTL = 7*24h`, up to `MaxTokensCeil = 2000`) shares the 200 MB evicting cache with counters and sessions. Under pressure the paid cache is destroyed, every reading regenerates, provider cost spikes — and the regenerated calls then hit the 8 s abort (F-11). At the measured 2 880 readings/day ceiling this is 40–80 MB per TTL window, so the trigger is **plausible, not yet proven**. | `ai.go:29-31,665`; `docker-compose.yml:33-35` | separate cache instance (see F-05) + explicit `maxmemory` per role |
| **F-44** | Migration `033` is a **no-op** on the shipped `029` (which never created the FK it targets) yet its `down` refuses to run; `schema-ready.sh:399-405` actively *requires* the FK's absence. The schema contract and the migration disagree. | `033_payment_webhook_audit.*`, `schema-ready.sh:399-405` | reconcile: either create the FK in a new migration or retire 033 |
| **F-45** | No daily cap on anonymous fingerprints; disabled users remain in the public share lookup and in push/referral background queries. | `auth` fingerprint path, `share.go`, `push.go:406-447` | add the cap; filter `disabled_at IS NULL` in every background query |
| **F-46** | Migration 034's quarantine rows are not moved on account merge. | `034_*`, `link.go:278` | move or re-point them in the merge transaction |
| **F-47** | The push endpoint policy permits arbitrary public hosts/ports. | `push.go` validation | allow-list known push endpoints |

---

## 7. LOW / INFO

| ID | Finding |
|----|---------|
| **F-48** | `HandleList` pulls `left(interpretation, 131073)` chars **per row** but emits 160 bytes → a 370-row history query moves ~48 MB to serve ~60 KB. |
| **F-49** | 112 of 175 Go tests SKIP without `DATABASE_URL`; only CI exercises the e2e suite (local `go test` is misleadingly green). |
| **F-50** | 5 routes have no rate-limit rule (`/v1/cards/{id}`, `/v1/plans`, `/v1/entitlements/me`, `/v1/streak/me`, `/v1/ab/me`) and there is no route-vs-rule invariant test, so adding a route without a rule is invisible. |
| **F-51** | `sw.js` `startsWith("/cards/")` matches the SSR `/cards/[id]` **page**, so card pages are CacheFirst forever; `VERSION` is still `"taro-v1"` and nothing enforces a bump. |
| **F-52** | The web prod image ships all devDependencies and `node_modules` wholesale → 1.27 GB image, larger supply-chain surface (integrity itself is fine: `npm ci` + lockfileVersion 3, 0 advisories). |
| **F-53** | `nginx-tls.conf` is a ~200-line copy of `nginx.conf`; the CI guard is a single `grep -Fq 'location = /readyz'`. The two files will drift. |
| **F-54** | Refund and push admin side effects write no `admin_audit` rows. |
| **F-55** | The AI cache survives account deletion; sensitive responses carry no `Cache-Control` directives. |
| **F-56** | `fwdHeaders` overwrites `Origin`/`Referer`, neutering `auth.originOK` for proxied traffic — **verified NOT browser-exploitable**: `apierr.Decode` requires `application/json` and no route sets CORS headers, so the preflight blocks it. Recorded so it is not re-raised as a login-CSRF. |
| **F-57** | **Adversarial correction:** the claim "a VAPID private test key is committed and present in history" is **NOT REPRODUCIBLE at HEAD** — `.env.example:19-20` ships `CHANGE_ME_GENERATE_YOUR_OWN` and no `BEl…` private-key literal exists in any tracked file. `gitleaks` reports 21 hits, all placeholders (18 in test files, 1 CI self-match). Treated as refuted; the CI secret gate (F-17) is the real problem. |
| **F-58** | **MEDIUM · CONFIRMED (found during A02 remediation).** `drainOnce` claims a reading **globally**, so the AI worker test is cross-package flaky. `TestE2EWorkerPersistsTerminalFallbackWithoutAI` (`ai_e2e_test.go:545-582`) calls the unexported `drainOnce`, whose claim query (`worker.go:149`) selects *any* eligible `pending` reading; with `workerBatchSize = 1` a foreign leftover row is claimed instead and the assertion sees `status=pending attempts=0`. Reproduced on a clean PG16+Redis under `go test -race -count=1 ./...`; passes in isolation, passes on re-run → scheduling-dependent flake. **Impact:** a CI that reports randomly trains the team to re-run red builds, destroying the signal (feeds F-17/F-18). **Fix:** scope the drain to the test's own reading (unique marker + bounded retry) or give worker tests their own schema. **Test:** seed a foreign `pending` reading, then `go test -race -count=3 ./internal/ai/ ./internal/readings/` must stay green. |

---

## 8. ARCHITECTURE

The layering is genuinely conventional (handler → service → pgx, DTO-free SQL, no ORM), and the dependency direction is clean. The architectural risks are structural, not stylistic:

1. **Two sources of truth for entitlements.** A live `AuthorizeReading` + receipts path and a dead `entitlements.Service`/`GrantBonusDays` path with *different, weaker* rules (F-38). Any future contributor will pick the wrong one.
2. **Redis is simultaneously** session store, CSRF store, rate limiter, circuit breaker, entitlement cache, and 7-day response cache — under one 200 MB `allkeys-lru` budget (F-05, F-43). This single decision couples security, availability and cost.
3. **The BFF silently drops upstream semantics** (F-13), so the Next layer is not a transparent proxy: cookie rotation, retry hints and SSE terminal states all die at that boundary. Three of my HIGH findings live exactly on this seam.
4. **The Go API is reachable two ways** (`/api/*` via Next, `/v1/*` directly from nginx), so Next is *not* a mandatory hop and any Next-side control is bypassable by construction (F-21, F-14).
5. **A single-goroutine, tick-driven worker** as the only path from "reading created" to "reading delivered" (F-06) — a capacity ceiling disguised as a scheduler detail.
6. **Configuration is not typed or centralized**: 15 required env vars with no schema, a 6-var gap in `.env.example` (F-32), and 5 admin-editable knobs that nothing reads (F-37).

---

## 9. SECURITY

- **Authentication** is sound in structure: HttpOnly JWT + CSRF double-submit + `session_version` invalidation, and tests correctly **fail** (not skip) on a missing `JWT_SECRET`. Admin auth uses password + `ADMIN_API_TOKEN` with `admin_session_version` — no replay.
- **Authorization**: one real gap — the refund tests mount the handler without `RequireAdmin` (F-18.1), so the money endpoint's authz is untested. The IDOR surface is otherwise covered by `user_id` predicates in SQL; the notable exception is the *share* path (by design) and the disabled-user leak (F-45).
- **Rate limiting is defeatable** (F-05) and **partly bypassed by design** (F-21, F-50).
- **No SSRF**: the only server-initiated HTTP is to the configured AI provider and Telegram; the push endpoint policy is the one permissive spot (F-47).
- **Injection**: no SQL injection found — all queries are parameterized; the one dynamic `ORDER BY` set is allow-listed. No command injection (no shell-outs with user data). No unsafe HTML/`dangerouslySetInnerHTML` with user data. Path traversal is blocked by the `CardArt` allowlist — which is exactly the allowlist that is wrong (F-04).
- **Secrets**: no real secret in tracked files (F-57). The real exposure is `.env` on the VPS plus a CI gate that cannot find anything (F-17).
- **Headers**: no XFO in prod, two CSPs (F-14).
- **Error leakage**: provider bodies into `ai_logs` (F-23); `apierr` otherwise returns stable codes.
- **Abuse**: the safety filter is bypassable (F-10); `payment_webhook_events` is write-only (F-09); the pending-invoice multi-link path is API-only abuse (F-26).

---

## 10. DOMAIN / MATHEMATICS

The tarot domain is where the STAIR template's geometry mandate lands.

**Draw algorithm — VERIFIED CLEAN.** Seeded PRNG, Fisher-Yates-style selection, position assignment, uniqueness and `reversed` distribution were re-derived and statistically checked at HEAD: no duplicates, no out-of-range positions, correct reversal probability, deterministic for a given seed, and `algo_version` is stored with each reading. The card catalog is 78 rows with ids 0-77, names, meanings, reversed meanings and image keys consistent with the schema.

**Quota/entitlement arithmetic — DEFECTIVE.** Three of the four arithmetic paths have proven races: subscription extension (F-07, lost update on `MAX`), referral monthly cap (F-24, check-then-act), and the daily-slot reservation (F-12, consumed before the work, never released). The fourth — `entitlements` daily/love counters — is correctly serialized by the `ON CONFLICT DO UPDATE` row lock. So the *same codebase* contains both the right and the wrong pattern for the same class of problem.

**Billing date math** is sound (`GREATEST(COALESCE(MAX(valid_until), now()), now()) + interval`), and the "days are never lost on merge" property is provable from the `revoked`-row ordering (F-25) — the loss is in *provenance*, not in duration.

**Fixed-window counters** are mathematically fine in isolation (`INCR` + `EXPIRE` at first hit) but wrong under eviction (F-05): the window is a function of key existence, not of time.

**Time-base consistency** is broken (F-35): a month key computed in server-local time against MSK-computed everything else is an off-by-one-day defect on the cap boundary.

**Probability surfaces**: reversal is cosmetic (rendering), so a distribution error would be cosmetic; still, the draw was verified.

**The real domain defect is capacity, not arithmetic**: the correct draw runs at 2 per minute (F-06).

---

## 11. DATABASE

- **Migrations**: all 34 apply cleanly in order on a virgin PG16 (`dirty=f`, 27 tables) — verified today. `golang-migrate` is atomic per file and marks `dirty` correctly; `migrate.sh down` floors the target at 28, so the older "down preflight omits 002/006/014/019/024" claim is **STALE**. Remaining issues: `recover-dirty.sh` without `--single-transaction` (F-27), the 033/029 disagreement (F-44), no pre-migration backup (F-16).
- **Integrity**: FK `single_entitlements.consumed_reading_id` survives `DELETE FROM users` via single-statement cascade. Quarantine rows (034) leak on merge (F-46). Disabled users leak into background queries (F-45).
- **Transactions/locks**: two proven lock-order inversions (F-08) and two proven read-modify-write races (F-07, F-24). `FOR KEY SHARE` does not break the reading counters; worker claim/lease/token fencing is sound; `ai.workerBatchSize` is the only capacity defect.
- **Queries**: `HandleList` over-fetches by 3 orders of magnitude (F-48); `/v1/diary/export` is bounded by an explicit 5 000-entry guard but then buffered whole in a 1 GiB container (F-33). `admin_accounts` emptiness breaks deploy-mode readiness (F-28).
- **Indexes**: no missing-index scan was completed against production data volumes — see §27.

---

## 12. API

30 distinct public `/v1/*` paths, 11 admin, 22 Next `route.ts`. Every mutating Go route is CSRF-checked; `originOK` is neutered for proxied traffic but not exploitable (F-56). Notable per-endpoint results are in the matrix below. `DisallowUnknownFields` is used exactly once in the whole API, so unknown-field mass assignment is possible wherever a struct is bound from a body — no concrete exploitable instance was found, but the pattern is unguarded.

---

## 13. FRONTEND

The Next layer is the highest-density defect zone: 3 of my HIGH findings and 5 MEDIUMs live on the Next↔Go seam. Root cause is a missing "relay" abstraction (F-13) plus three independent claims about features whose server side does not exist (push receive F-29, paywall copy knobs F-37, analytics F-34). SSR/hydration is otherwise sound, and `proxy.ts` **is** correctly registered as Next 16 middleware (functional probe: `/profile|/diary|/history|/reading/*` → 307 to `/spreads` without the cookie, 200 with it) — the earlier "middleware is dead" claim is **REFUTED**; note that `middleware-manifest.json` has an empty `sortedMiddleware` even on a successful build and must never be used as proof.

---

## 14. PERFORMANCE

- **Worker throughput 2.00 readings/min** (F-06) — the dominant capacity fact.
- Redis-down latency 1.714 s per fail-open request (F-42); 300 concurrent → 962 ms each.
- `HandleList` 131 073 chars/row vs 160 bytes emitted (F-48).
- Diary export buffered in memory (F-33); 1.27 GB web image (F-52).
- No `ReadHeaderTimeout`/`IdleTimeout` (F-41); no pool/FD/goroutine budgets anywhere; 300 pinned goroutines per burst measured.
- Go `-race` clean, `go vet` clean, `tsc` clean, eslint clean, 21 web tests in 1.64 s.

---

## 15. INFRASTRUCTURE

Deploy is blocked (F-01), the admin plane is dead (F-02), there is no rollback (F-16), the prod env contract is undocumented and incomplete (F-32), the web image cannot write its cache (F-03), no prod host ports are published (`ports: !reset []` — good), the dev config publishes them (bad), health checks exist for db/cache/api but `apierr.Recover` can kill a background job while health stays green (F-19), and the whole observability layer is 10 `log.Printf` (F-20).

---

## 16. TESTING

The pipeline is green and the product is broken: `go test -race` 14/14, 34/34 migrations, `tsc` clean, eslint clean, 21 web tests — with 2 CRITICAL and 18 HIGH defects present. Root causes, in order of leverage:

1. Tests assert **current behaviour**, not invariants (285 status-code checks, 18 `Contains`, 0 deep-equality).
2. Authorization is not exercised on the money endpoints (F-18.1).
3. Environment masks failure paths (`TG_BOT_TOKEN`, F-18.2) — the classic "test passes because the feature is off".
4. Tests share production infrastructure and **delete production keys** (F-18.3).
5. Non-idempotent tests (`-count=2` fails, F-18.4).
6. Zero fuzz/property targets, including for the draw algorithm, the filter, and the window counters — all three of which are pure functions begging for property tests.
7. `cmd/api` has 0 % coverage while hosting the worker and the ticker (F-19).

**Critical code paths with no meaningful test:** webhook↔delete concurrency; concurrent subscription grants; the SSE terminal-frame contract (both sides); the limiter's `failClosed` branch and its eviction behaviour; `apierr.Recover` inside tickers; the pending-invoice retry path; the refund-revoke-after-merge path; `adminctl` schema gating; push receive.

---

## 17. DOCUMENTATION

False claims found (code is the source of truth): `web/public/cards/README.md` describes 56 card images in an empty directory (F-04); `deploy/README.md` claims images are digest-pinned (6 of 9 float) and documents no rollback (F-15, F-16) and a `frame-ancestors` behaviour nginx does not implement (F-14); `docs/…/02-interaction-next-go.md` names Redis as the limit enforcer while `ent:*` keys are write-only (F-38); the analytics/KPI docs are built on events that are never emitted (F-34). Undocumented requirements: the 15 required prod env vars (F-32) and the fact that `/v1/*` bypasses Next entirely.

---

## 18. ATTACK MATRIX

| Attack | Entry point | Preconditions | Result | Confirmed? | Severity |
|---|---|---|---|---|---|
| Rate-limit reset via cache pressure | any limited route | cache near 200 MB | 40 req/2.1 s vs cap 30/60 s | **YES (probe)** | HIGH |
| Auth brute force | `POST /v1/auth/*` | as above | 20/min cap defeated per window | **YES (mechanism)** | HIGH |
| Paid-generation cost abuse | `POST /v1/readings` | as above | 10/min cap defeated | **YES (mechanism)** | HIGH |
| Global IP-bucket DoS | Next-proxied `/v1/auth/`, `/v1/spreads`, `/v1/share` | one abusive IP | 429s **all** Next-proxied users | **YES (code)** | MEDIUM |
| Double-click double-grant | two concurrent webhooks | Telegram retry race | 2 paid months → 30 days | **YES (11/12)** | HIGH |
| Silent money loss | webhook as deadlock victim | 40P01 + retry | HTTP 200, 0 audit rows, no entitlement | **POSSIBLE** (chain real, victim not observed) | HIGH |
| Safety-filter evasion | any text input | none | 16/21 hostile strings pass | **YES** | HIGH |
| Clickjacking | any prod page | none | no XFO at all | **YES** | HIGH |
| Path traversal via art key | `CardArt` | none | blocked — fails closed to fallback | **YES (blocked)** | INFO |
| SQL injection | all queries | — | none found; all parameterized | **NO** | — |
| IDOR | `/v1/readings/{id}`, `/v1/diary/{id}` | valid session | blocked by `user_id` predicates | **NO** | — |
| Login CSRF via proxy Origin rewrite | `/v1/auth/*` | browser | blocked by `application/json` requirement + no CORS | **NO** | INFO |
| SSRF | push endpoint subscribe | valid session | arbitrary public host/port accepted | **LIKELY** | MEDIUM |
| Slowloris | Go API | dev/staging published ports | no read/idle timeouts | **LIKELY** | MEDIUM |
| Admin-plane outage | any deploy | `api-admin` recreate | admin 502 + 4 cron jobs dead | **YES (live)** | CRITICAL |
| Release-path outage | any tagged deploy | one backup taken | deploy aborts at the untracked-file gate | **YES** | CRITICAL |

---

## 19. INVARIANT MATRIX

| Invariant | Enforced at | Bypassable? | Test exists? | Result |
|---|---|---|---|---|
| ≤ Max requests per window per key | `ratelimit.go:24-28` (Redis `INCR`) | **Yes** — eviction resets the window | 1 test, `-count=2` fails | **BROKEN** |
| Subscription extension is atomic | `payments.go:605-622` | **Yes** — unlocked `MAX()` | no concurrency test | **BROKEN** |
| Referral month ≤ 30 days | `referral.go:179-186` | **Yes** — check-then-act | no | **BROKEN** |
| A reading is delivered for every consumed entitlement | `readings.go:459` + worker | **Yes** — stall consumes, no refund | asserts DB status only | **BROKEN** |
| Reading throughput ≥ demand | `ai/worker.go:20` + `main.go:90` | **Yes** — hard ceiling 2/min | no | **BROKEN** |
| Refund revokes the entitlement | `payments.go:1131-1144` | **Yes** — after a merge (F-25) | env-masked (F-18.2) | **BROKEN** |
| SSE stream ends in a terminal state | client `api.ts:73-97` | **Yes** — pending-without-done reads as success | untested both sides | **BROKEN** |
| Every seeded art key renders | `CardArt.tsx:11-12` vs seed | **Yes** — 0/78 | none | **BROKEN** |
| Crisis text is caught | `filter.go:21-29` | **Yes** — 16/21 | corpus absent | **BROKEN** |
| Money events are exactly-once | webhook idempotency | Partially — duplicate charges recorded, never read | no | **AT RISK** |
| Draw is uniform, unique, deterministic | draw + seed | No | statistical only | **VERIFIED** |
| Entitlement counters serialize | `ON CONFLICT DO UPDATE` row lock | No | yes | **VERIFIED** |
| Worker claim/lease/fencing | `ai/worker.go` | No | yes | **VERIFIED** |
| Age gate precedes onboarding | — | — | — | **BROKEN** (F-40) |

---

## 20. API SECURITY MATRIX

| Endpoint | Auth | Authz | Validation | Rate limit | IDOR | Injection | Result |
|---|---|---|---|---|---|---|---|
| `POST /v1/auth/anon` | none (by design) | — | yes | 20/min IP (evictable) | n/a | no | OK, limiter weak |
| `POST /v1/auth/telegram` | Telegram hash | — | yes | 20/min, fail-closed | n/a | no | OK |
| `POST /v1/auth/link` | JWT | own account | yes | 20/min | merge-only | no | F-25 |
| `POST /v1/readings` | JWT + CSRF | own | yes | 10/min user (evictable) | no | no | F-06, F-11, F-12 |
| `GET /v1/readings/{id}` | JWT | `user_id` | yes | 30/min | no | no | OK |
| `POST /v1/share` | JWT | own | yes | 60/min (evictable) | no | no | OK |
| `GET /v1/share/{token}` | none | by token | yes | shared with `/v1/share` | by design | no | F-45 (disabled users) |
| `POST /v1/share/revoke` | JWT | own | yes | 30/min | no | no | **no Next route/UI** |
| `POST /v1/diary` | JWT + CSRF | own | yes | 30/min | no | no | OK |
| `GET /v1/diary/export` | JWT | own | 5 000 cap | 30/min | no | no | F-33 |
| `POST /v1/payments/stars/invoice` | JWT | own | yes | 10/min | no | no | F-26 |
| `POST /v1/payments/stars/webhook` | Telegram secret | — | yes | none (external) | n/a | no | **F-07, F-08** |
| `POST /v1/referral/apply` | JWT | own | yes | 10/min | no | no | F-24, no cycle/fingerprint cap |
| `POST /v1/push/subscribe` | JWT | own | endpoint policy | 20/min | no | no | F-47 |
| `GET /v1/cards/{id}`, `/v1/plans`, `/v1/entitlements/me`, `/v1/streak/me`, `/v1/ab/me` | mixed | own/none | yes | **none** | no | no | F-50 |
| `POST /v1/admin/refund` | admin JWT | `RequireAdmin` | yes | 30/min | n/a | no | **authz untested (F-18.1)**, no `admin_audit` (F-54) |
| `POST /v1/admin/login` | password + token | — | yes | 20/min | n/a | no | OK |
| `POST /v1/admin/rotate-seasonal` | admin | `RequireAdmin` | yes | 30/min | n/a | no | F-35 (server-local time) |
| `GET /v1/admin/payments` | admin | `RequireAdmin` | yes | 30/min | n/a | no | `payment_webhook_events` unread (F-09) |

---

## 21. DATABASE INTEGRITY MATRIX

| Entity | PK | FK | Unique | Check | Transaction | Race risk | Result |
|---|---|---|---|---|---|---|---|
| `users` | uuid | — | `tg_id` | — | merge/delete | **lock-order inversion** | F-08 |
| `subscriptions` | id | user, plan, payment | ❌ none on `(payment_id)` | — | grant | **lost update on `MAX(valid_until)`** | F-07, F-25 |
| `payments` | id | user | charge ids | — | webhook | **deadlock with user delete** | F-08 |
| `single_entitlements` | id | user, payment | `(payment_id)` | — | authorize | consumed-row FK | verified safe |
| `entitlements` | user_id | user | PK | — | referral | **check-then-act cap** | F-24 |
| `reading_authorization_receipts` | reading | reading | PK | — | authorize | correct | verified safe |
| `readings` | id | user, spread | `(idempotency_key)` | — | create/claim | lease fencing | verified safe; **throughput ceiling** F-06 |
| `referrals` | id | users | — | status enum | apply | — | F-24 |
| `diary_entries` | id | user, reading | — | — | CRUD | — | OK |
| `push_subscriptions` | id | user | endpoint | — | subscribe | — | F-45, F-47 |
| `payment_webhook_events` | id | payment | — | — | webhook | — | **no reader** F-09 |
| `quarantine` (034) | id | — | — | — | — | — | **leaks on merge** F-46 |
| `admin_accounts` | id | — | login | — | login | — | breaks deploy verify (F-28) |
| `schema_migrations` | version | — | PK | — | per file | — | atomic; 033/029 mismatch (F-44) |

---

## 22. MASTER FINDINGS TABLE

| ID | Sev | Status | Layer | Category | Finding | Evidence | Exploitability | Impact |
|---|---|---|---|---|---|---|---|---|
| F-01 | CRITICAL | CONFIRMED | CI/CD | deploy gate | `backups/` untracked aborts every deploy | `.gitignore:1-10`, `deploy.yml:84` | deterministic | no releases possible |
| F-02 | CRITICAL | CONFIRMED | Infra | topology | `admin-access` netns desync | `docker-compose.yml:133`, live `ip -o addr` | on every recreate | admin + 4 crons dead |
| F-03 | HIGH | CONFIRMED | Web/Docker | perms | root-owned `.next/cache` → EACCES | `web/Dockerfile:20,25`; 210/200 rejections | unauthenticated | images 400, log flood |
| F-04 | HIGH | CONFIRMED | Frontend | contract | art allowlist vs seed: 0/78 | `CardArt.tsx:11-12`, `002_seed.up.sql` | every card | product visually broken |
| F-05 | HIGH | CONFIRMED | Security | rate limit | window reset by `allkeys-lru` | probe: 40 req/2.1 s vs 30 | memory pressure | brute force + cost abuse |
| F-06 | HIGH | CONFIRMED | Perf | capacity | worker ceiling 2.00 readings/min | `worker.go:20`, probe 8/10 in 4 min | load | backlog unbounded |
| F-07 | HIGH | CONFIRMED | DB | lost update | unlocked `MAX(valid_until)` | `payments.go:605-622`, 40/40 | 2 concurrent webhooks | paid days lost |
| F-08 | HIGH | CONFIRMED | DB | deadlock | webhook ↔ `DELETE /v1/me` 40P01 | `payments.go:154-164`, `me.go:78-87` | concurrent | 500 + silent re-login; possible silent money loss |
| F-09 | HIGH | CONFIRMED | Obs | money | `payment_webhook_events` never read | module-wide grep | — | duplicate charges unactioned |
| F-10 | HIGH | CONFIRMED | Domain | safety | filter bypassable; self-match | `filter.go:10,16-29` | trivial | safety gate illusory |
| F-11 | HIGH | CONFIRMED | Resilience | timeout | 8 s covers TTFT + generation | `ai.go:27,114,742`, 12 s mock | slow provider | aborts + breaker storm |
| F-12 | HIGH | CONFIRMED | Domain | money | entitlement consumed on stall | `readings.go:459` | provider stall | paid, no reading |
| F-13 | HIGH | CONFIRMED | Cross-layer | headers | 18/22 routes drop `Set-Cookie` | measured 18 vs 3 | authenticated writes | CSRF self-heal broken |
| F-14 | HIGH | CONFIRMED | Security | headers | no XFO; 2 CSPs | `nginx.conf:30-37` | any page | clickjacking |
| F-15 | HIGH | CONFIRMED | Supply chain | EOL | go1.25/node20/alpine3.20/nginx1.27; 6 float | image `Created` dates | — | unpatched, irreproducible rollback |
| F-16 | HIGH | CONFIRMED | Deploy | rollback | no pre-migration backup, no rollback | `deploy.yml:94-105` | bad migration | one-way door |
| F-17 | HIGH | CONFIRMED | CI | blind spots | no vuln/secret/coverage/compose gates | `ci.yml` | — | green pipeline, broken product |
| F-18 | HIGH | CONFIRMED | Tests | test-the-tests | authz/env/cache/count defects | `extra_e2e_test.go:28`, `ci.yml:58-61` | — | money paths untested |
| F-19 | HIGH | CONFIRMED | Resilience | panic | `Recover()` outside the ticker loop | `main.go:92-109`, `apierr.go:73` | any panic | job dies silently |
| F-20 | HIGH | CONFIRMED | Obs | telemetry | 10 `log.Printf`, no metrics/IDs | whole `api/` | — | undetectable incidents |
| F-21 | MED | CONFIRMED | Cross-layer | IP | `X-Real-IP` not forwarded | `proxy.ts:41-57` | one IP | shared buckets (**P0 claim corrected**) |
| F-22 | MED | CONFIRMED | API | contract | SSE pending read as success | `readings.go:846-852`, `api.ts:73-97` | provider stall | blank page, quota spent |
| F-23 | MED | CONFIRMED | Security | leakage | provider 4xx body → `ai_logs` | `ai.go:471-473` | any 4xx | provider text in DB/UI |
| F-24 | MED | CONFIRMED | DB | race | referral cap check-then-act | `referral.go:179-186`, 10/10 | 2 concurrent | 33 > 30 days |
| F-25 | MED | CONFIRMED | DB | integrity | merge can orphan refund revoke | `link.go:255-259` | merge+refund | refund revokes nothing |
| F-26 | MED | CONFIRMED | Payments | abuse | multiple Telegram invoice links | `payments.go:957-964` | API client | duplicate invoices |
| F-27 | MED | CONFIRMED | Migrations | atomicity | `psql -f` without `-1` | `recover-dirty.sh:156` | interrupted recovery | half-applied schema |
| F-28 | MED | CONFIRMED | Ops | readiness | empty `admin_accounts` fails deploy verify | `schema-ready.sh:606-620` | fresh DB | deploy blocked; `adminctl` ungated |
| F-29 | MED | CONFIRMED | Frontend | feature | no push receive path | `sw.js` | push | UI claims a feature that cannot fire |
| F-30 | MED | CONFIRMED | Web | metadata | no `metadataBase`; token logs; `$host` | `layout.tsx`, nginx log_format | share | dead previews, token leakage |
| F-31 | MED | CONFIRMED | Build | config | `NEXT_PUBLIC_BASE_URL` optional | `Dockerfile:5,14` | plain build | build failure / localhost sitemap |
| F-32 | MED | CONFIRMED | Deploy | config | 6 of 15 required vars missing from `.env.example` | verified today | fresh VPS | deploy fails mid-run |
| F-33 | MED | CONFIRMED | Web | memory | export buffered in 1 GiB container | `diary/export/route.ts` | 5 000 entries | OOM risk |
| F-34 | MED | CONFIRMED | Analytics | drift | KPI events never emitted | `analytics.ts:46-47` | — | funnel is fiction |
| F-35 | MED | CONFIRMED | Domain | time | server-local vs MSK month keys | `referral.go:178` | month boundary | off-by-one-day cap |
| F-36 | MED | CONFIRMED | Payments | A/B | float accepted, int unmarshalled | `payments.go:352` | decimal value | A/B silently off |
| F-37 | MED | CONFIRMED | Config | dead knobs | 5 `app_config` keys never read | `admin.go` | — | no referral kill switch |
| F-38 | MED | CONFIRMED | Arch | dead code | weaker second quota implementation | `entitlements.go:763-779` | — | foot-gun |
| F-39 | MED | CONFIRMED | Frontend | auth | `retryAuth` never called | `auth.ts:127` | any 401 | permanent auth failure; JWT after delete |
| F-40 | MED | CONFIRMED | Frontend | legal | Onboarding paints over AgeGate | `layout.tsx:42-43` | first visit | 18+ notice unreachable |
| F-41 | MED | CONFIRMED | Net | hardening | no server timeouts | `main.go:183`, `admin/main.go:123` | dev ports | Slowloris / conn exhaustion |
| F-42 | MED | CONFIRMED | Resilience | latency | 1.714 s per fail-open request | probes today | Redis down | 300 pinned goroutines/burst |
| F-43 | MED | LIKELY | Cache | coupling | 7-day AI cache shares evicting cache | `ai.go:665` | memory pressure | paid cache destroyed |
| F-44 | MED | CONFIRMED | Migrations | contract | 033 no-op on 029; down refuses | `033_*`, `schema-ready.sh:399` | — | schema contract incoherent |
| F-45 | MED | CONFIRMED | Auth | leak | no anon daily cap; disabled users in bg queries | `share.go`, `push.go:406-447` | — | farm + stale targeting |
| F-46 | MED | CONFIRMED | DB | leak | 034 quarantine not moved on merge | `034_*`, `link.go:278` | merge | orphaned rows |
| F-47 | MED | LIKELY | Security | SSRF-ish | push endpoint allows any host/port | `push.go` | valid session | internal probing |
| F-48 | LOW | CONFIRMED | Perf | over-fetch | 131 073 chars read, 160 bytes sent | `readings.go:1081` | history page | ~48 MB per query |
| F-49 | LOW | CONFIRMED | Tests | signal | 112/175 tests skip locally | test files | no DB | misleading local green |
| F-50 | LOW | CONFIRMED | Security | coverage | 5 routes without a rate-limit rule | `ratelimit.go:60-68` | — | unmetered endpoints |
| F-51 | LOW | CONFIRMED | Web | caching | `/cards/` page is CacheFirst; VERSION frozen | `sw.js:41` | — | stale card pages |
| F-52 | LOW | CONFIRMED | Supply chain | size | devDeps in prod image (1.27 GB) | `web/Dockerfile:21` | — | large surface |
| F-53 | LOW | CONFIRMED | Ops | drift | `nginx-tls.conf` copy; 1-grep CI guard | `deploy/` | — | config drift |
| F-54 | LOW | CONFIRMED | Admin | audit | no `admin_audit` for refund/push | `admin.go` | — | unaudited money actions |
| F-55 | LOW | CONFIRMED | Privacy | retention | AI cache survives account deletion | `ai.go:665` | — | data retained |
| F-56 | INFO | REFUTED-risk | Security | origin | Origin rewrite neuters `originOK` | `proxy.ts`, `apierr.Decode` | — | not exploitable (documented) |
| F-57 | INFO | REFUTED | Security | secrets | "VAPID private key committed" | `.env.example:19-20`, gitleaks | — | **not reproducible at HEAD** |
| F-58 | MEDIUM | CONFIRMED | Tests | isolation | `drainOnce` claims globally → AI worker test flakes across packages | `ai_e2e_test.go:545-582`, `worker.go:149` | scheduling-dependent | red builds teach re-runs |

**Totals: 2 CRITICAL · 18 HIGH · 28 MEDIUM · 8 LOW · 2 INFO = 58 findings** (F-58 добавлена при ремедиации A02, 2026-09-25). Positive verifications are listed in §23.

---

## 23. VERIFIED

- `go build`, `go vet`, `gofmt` clean; `go test -race -count=1 ./...` **14/14 packages pass** against a virgin PG16 + a production-configured Redis.
- All **34 migrations apply in order**, `dirty = f`, 27 tables; `golang-migrate` is atomic per file.
- `tsc --noEmit` clean, eslint clean, **21/21 web tests pass** in 1.64 s.
- Dependency integrity: `go mod tidy -diff` clean, `go mod verify` clean, `npm ci` lockfile in sync (v3, 675 entries), `npm audit --omit=dev` = 0.
- **Draw algorithm**: uniform, unique positions, deterministic per seed, correct reversal distribution, catalog 78/78 with ids 0-77.
- Entitlement daily/love counters **are** serialized (row lock via `ON CONFLICT DO UPDATE`).
- Worker claim/lease/token fencing is sound; `FOR KEY SHARE` does not break reading counters.
- No SQL injection, no command injection, no unsafe HTML, no XSS sink, no IDOR in the user-scoped queries; path traversal blocked by the art allowlist.
- Admin auth: password + token + `admin_session_version`; no replay. Tests correctly fail on a missing `JWT_SECRET`.
- `proxy.ts` **is** registered as Next 16 middleware (functional 307 probe) — the earlier "middleware is dead" claim is refuted.
- `web` CSP/XFO from `next.config.js` **are** baked into `.next/routes-manifest.json` (they are stripped later by nginx — F-14, a different bug).
- `migrate.sh down` floors the target at 28 — the older "preflight omits 002/006/014/019/024" claim is **stale**.
- No real secret in tracked files; no `BEl…` VAPID private key anywhere in the tree.
- `payment_webhook_events` is intentionally mismatch-only — recording everything is not a defect.
- Prod publishes no host ports (`ports: !reset []`).

---

## 24. BROKEN

Release path (F-01) · admin plane and all cron jobs (F-02) · card art (F-04) · rate limiting under memory pressure (F-05) · reading throughput (F-06) · subscription extension under concurrency (F-07) · delete/webhook concurrency (F-08) · duplicate-charge handling (F-09) · the safety filter (F-10) · healthy-generation tolerance (F-11) · entitlement refund on failure (F-12) · CSRF self-heal through the BFF (F-13) · clickjacking protection (F-14) · rollback (F-16) · observability (F-20) · observability of background jobs (F-19).

---

## 25. AT RISK

Money: silent-webhook-200 path (F-08 chain, not observed), provenance-losing merges (F-25), pending-invoice duplication (F-26), uncaptured duplicate charges (F-09). Availability: prod cache saturation (F-43, mechanism proven, trigger unproven), Redis-down latency amplification (F-42), worker backlog (F-06). Privacy: AI cache retention after deletion (F-55), share tokens in access logs (F-30), provider text in the DB (F-23). Process: 6/15 undocumented prod vars (F-32), green CI (F-17).

---

## 26. UNPROVEN

- That the webhook can be the deadlock victim in production (requires a specific plan mix and delete shape; the chain is real, the trigger was not reproducible).
- That the 200 MB cache reaches saturation under real traffic (mechanism proven with ballast; real fill rate unmeasured).
- That a real provider's p99 exceeds 8 s in production (proven with a healthy 12 s mock; the real distribution is unmeasured).
- Whether any of F-07/F-08/F-24 has already fired in production — no production telemetry exists to answer this (F-20).
- The correct severity of F-41 in production, since nothing is host-published there.

---

## 27. NOT AUDITED

- **Live-stack behaviour.** All containers were built 07:10–07:59Z against a 15:00Z commit; the stack was down. Every behavioural claim here is from source or from probes on throwaway infrastructure. *(Why: stale images; re-auditing live behaviour would produce false findings — that mistake was made and corrected once already.)*
- **Real Telegram Stars webhooks** (no provider credentials) — duplicate-invoice and refund-revoke chains are code-proven, not live-proven.
- **Real AI-provider behaviour** (no `OPENROUTER_API_KEY`) — timeouts, breaker and stall findings come from mocks.
- **Load/benchmark testing** — no k6/locust tooling exists in the repo, so no saturation curve, no connection-pool sizing, no cost-per-1000-readings.
- **Index adequacy against production data volumes** — no `EXPLAIN ANALYZE` against real cardinalities.
- **The morning 12-pass set** taken on a dirty tree (Engram #1036) was re-verified only where it fed the final register.
- **Third-party supply-chain deep dive** beyond version freshness, `npm audit`, `go mod verify` and `gitleaks` (no Trivy/Grype/SBOM scan — the tooling is not installed and installing it was out of scope).

---

## 28. FINAL CONCLUSION

TARO is a competently built, unusually well-tested codebase that **cannot currently ship and cannot currently be trusted with money**. The engineering quality inside each layer is above average — parameterized SQL, lease fencing, atomic migrations, correct counter serialization, real negative tests. The failures are almost entirely at the **seams and in the operational envelope**: the Next↔Go header contract, the shared LRU cache, the single-goroutine worker, the deploy topology, and the near-total absence of telemetry. That combination is why 2 CRITICAL and 18 HIGH defects coexist with a fully green pipeline.

The most dangerous property is not any single bug: it is that **money paths have no test coverage, no audit trail, and no telemetry** (F-09, F-18, F-20). A lost entitlement day (F-07) or a silently-acked webhook (F-08) is currently undetectable. The most expensive property is F-01+F-02: the release path and the admin plane are both broken, so even a perfect fix cannot reach production.

**Remediation order (this is the actionable verdict):**

**Wave 0 — unblock shipping (≈1 h, zero risk).** F-01 `.gitignore` + move `BACKUP_DIR` out of the worktree · F-02 replace `network_mode: service:api-admin` with a normal network + DNS upstream · F-03 chown `.next/cache` (or standalone output) · F-14 add `X-Frame-Options` · F-04 decide the art contract and commit the assets · F-32 document the 15 required prod vars and preflight them in `deploy.yml`.
*Nothing else can be deployed until Wave 0 lands.*

**Wave 1 — money integrity (≈2 days).** F-07 `pg_advisory_xact_lock` on the grant + unique index on `subscriptions(payment_id)` · F-08 one global lock order (users → payments) and purge Redis only after commit · F-09 `adminctl payments reconcile` + an alert on `duplicate_charge` · F-24 fold the cap check into the counter statement · F-25 never delete a payment-referenced subscription row · F-12 reserve-then-release the entitlement · F-11 split TTFT and generation deadlines and stop counting deadline aborts as provider failures · F-18 add `RequireAdmin` to the refund tests and `TG_BOT_TOKEN` to CI.
*Every one of these has a ready-made fix pattern already in the codebase.*

**Wave 2 — capacity and cache (≈1 day).** F-06 batch + shorter tick (target ≥ 60 readings/min) · F-05 move `rl:*`/`sess:*` to a `noeviction` instance · F-42 explicit Redis timeouts and a ~50 ms fail-open budget · F-48 stop over-fetching 131 073 chars/row.

**Wave 3 — web contracts and safety (≈2 days).** F-13 one `relay()` helper for all 22 routes + a header-survival test · F-21 forward `X-Real-IP` · F-22 require a terminal SSE frame · F-10 normalize before matching and fix the `SafeReplacement` self-match, with the 21-string corpus as a golden test · F-29 implement or retract push · F-30/F-31 `metadataBase` + required build args · F-39/F-40 `retryAuth` wiring and age-gate sequencing.

**Wave 4 — supply chain, deploy safety, observability (≈2 days).** F-15 go 1.27 / node 24 / alpine 3.22+ / nginx 1.29+, digest-pin the 6 floating bases · F-16 pre-migration backup + a real rollback job + non-interactive `migrate.sh down` · F-17 govulncheck, Trivy/gitleaks, golangci-lint, coverage gate, prod `compose config` in CI, build the web image in CI · F-19 move `Recover()` inside the loop and make it log · F-20 slog + request IDs + `/metrics` · F-41 server timeouts.

**Wave 5 — hygiene (≈3 days).** F-37/F-38 delete or wire the dead quota path and the 5 unread knobs · F-44/F-46/F-45/F-47 migration and query hygiene · F-35 one MSK time helper · F-36 int-only A/B validator · F-48–F-57 the LOW/INFO list, including the four false documentation claims.

**Add, in every wave:** the missing test layer — property/fuzz tests for the draw, the filter and the window counters; a route-vs-rate-limit-rule invariant test; a dedicated test Redis prefix or DB so no test can ever delete a production key again.

**Realistic estimate:** Waves 0+1 (correctness and shippability) ≈ 3 days of focused work and remove every CRITICAL and every money-path HIGH. Waves 2–4 (capacity, supply chain, observability) ≈ 5 days and are what make the system operable. Wave 5 is optional debt.

---

*Produced by a read-only forensic audit at `1217da4`. No production file was modified; all probes ran against throwaway PostgreSQL 16 / Redis 7 containers and scratch modules in `$TMPDIR`. Two findings (F-05, F-06) were recovered from a crashed session's abandoned probe harness and are newly proven.*
