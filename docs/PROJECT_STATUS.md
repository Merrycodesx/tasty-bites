# Project status (verified state)

Last verified: branch `feature/qr-sse-tv`, after the QR / SSE / TV stage. Source of truth for future sessions. "Verified" = an automated test or scripted run actually executed it. I (the AI) have **not** run any UI in a real browser, the Macle simulator or an Android TV; the owner reported running all three apps locally.

## Checks last run
| Check | Result |
|---|---|
| `go vet`, `gofmt -l` | clean |
| `go test -race ./...` (Postgres 16, disposable DB) | pass: app 25 incl. QR/TV/SSE, orders 9, config 2 |
| Mutation checks (4 deliberate breakages: unscoped customer stream, unscoped hub, ignored `active`, missing event) | each caught by a test |
| `e2e/e2e.mjs` (real API + real SSE client code, restart mid-run) | 25/25 |
| Migration 0001→0002 upgrade on a DB with existing data | pass |
| customer-miniapp / staff-web / tv-display: typecheck, vitest, production build | pass (5 / 7 / 7 tests) |
| `npm run build:macle` | builds; Macle config identical to original, appID `444` untouched |
| `npm audit --omit=dev` (all 3 apps) | 0 vulnerabilities (dev-tooling advisories not triaged) |

## Feature status
Legend: PASS verified · PARTIAL · MOCK · FAIL · NOT IMPLEMENTED · BLOCKED. Priority C/H/M/L.

### Customer Mini-App
| Feature | Status | Evidence / what remains | Pri |
|---|---|---|---|
| Menu, categories, search, variants, extras, notes | PARTIAL | API + pricing PASS (tests); UI builds, never browser-tested by me | M |
| Cart and totals | PARTIAL | Client total is an estimate only; authoritative total PASS (server) | M |
| Checkout validation per order type | PASS (API) / PARTIAL (UI) | `orders_test`, `app_test` | M |
| QR table identification | PARTIAL | Backend PASS (tests, e2e); UI implemented, not browser-tested; **BLOCKED**: how Telebirr opens a Mini-App from a QR / passes `?t=` is unverified | C |
| Order creation, idempotency | PASS | 15 parallel identical requests → 1 order; e2e | – |
| Live order tracking | PASS (API/client lib) / PARTIAL (Macle) | e2e; fetch streaming in Macle unverified, polling fallback exists untested there | H |
| Order history | NOT IMPLEMENTED | only the latest order is remembered | M |
| Loading/empty/error/retry states | PARTIAL | implemented; unverified visually | M |
| Macle build/packaging | PARTIAL | `build:macle` output; simulator test pending (owner) | C |
| Fake payment / demo data in prod path | none found | seed items are dev-only (`SEED_DEV_DATA`) | – |
| Telebirr payment | NOT IMPLEMENTED / BLOCKED | provider interface only; needs credentials, docs, approval | C |

### Staff web
| Feature | Status | Evidence / remaining | Pri |
|---|---|---|---|
| Login/logout/sessions | PASS | tests; logout kills token and open streams | – |
| Backend role enforcement | PASS | role×endpoint matrix probed live + tests | – |
| Staff/role/password management | NOT IMPLEMENTED | no way to create staff or change passwords except seed/SQL | **C (pilot blocker)** |
| Waiter order entry | PARTIAL | works; waiter is optional per new requirements | L |
| Kitchen queue and statuses | PASS (API, live) / PARTIAL (UI) | e2e; kitchen no longer sees customer PII | – |
| Cashier billing, cash payment | PARTIAL | Cashier view highlights unpaid orders and confirms cash receipt; cashier can manually enter an order/table when QR was not scanned. Telebirr is still unconfigured; unpaid-completion rule remains a known defect | H |
| Receipts | NOT IMPLEMENTED | | H |
| Menu management | PARTIAL | edit price/availability, add items; no categories/variants/extras/archive | H |
| Tables + QR admin, TV displays, TV settings | PASS (API) / PARTIAL (UI) | tests + e2e; UI not browser-tested | – |
| Reports | PARTIAL | Dashboard supports today, this month, and all time; paid revenue uses settlement time and branch timezone, with top item/table rankings and hour/day/month bars. CSV export remains absent. Implementation not re-verified | M |
| Inventory/recipes/stock | NOT IMPLEMENTED | | M |
| Audit log | PARTIAL | cancels, cash, menu/table/display/settings recorded; no UI; menu changes lack old/new values | M |
| Settings (payment timing etc.) | PARTIAL | only TV settings; configurable dine-in payment timing not built | H |

### Backend / data
| Feature | Status | Evidence / remaining | Pri |
|---|---|---|---|
| Validation, server pricing, snapshots | PASS | tests; price change leaves old order totals unchanged | – |
| Order state machine | PARTIAL | rules enforced; gaps: unpaid completion; kitchen can set order `preparing` while items stay `queued` | H |
| Auth security | PARTIAL | bcrypt, hashed 12 h tokens, constant-cost unknown-user login; no lockout, no password policy/change, sessions never purged; login limiter keys on socket address (all users share a bucket behind a reverse proxy) | H |
| Migrations | PASS | fresh + upgrade tested; forward-only | – |
| Real-time (SSE) | PASS | scoping, replay, resync, reconnect, auth expiry, cleanup | – |
| Payment records | PARTIAL | cash only; no `payment_events`/`refunds`/reconciliation | H |
| Reliable events / outbox | PARTIAL | events written in the order transaction; fan-out in-process → single API instance only | M |
| Docker / env | PARTIAL | `docker-compose.yml`/Dockerfile written, **never run** | H |
| `go.sum`, CI, lint | NOT IMPLEMENTED | run `go mod tidy`; no CI | H |
| HTTPS, security headers, CSP | NOT IMPLEMENTED | terminate TLS in a reverse proxy; none configured | C (prod) |
| Logging/monitoring | PARTIAL | structured error logs only; no request log/metrics | M |
| Backups/restore | NOT IMPLEMENTED | | **C (prod)** |
| OpenAPI | NOT IMPLEMENTED | | M |

### Public TV
| Feature | Status | Evidence | Pri |
|---|---|---|---|
| Display app, pairing, revocation, data filtering, lifecycle rules | PASS (API/client lib) / PARTIAL (visual) | tests + e2e; never viewed on an actual Android TV browser (device test pending) | H |

## Is it complete? No.
**Before a safe pilot:** staff/password management; Macle simulator verification (launch from QR, fetch, streaming, storage); decide/implement dine-in payment timing and fix unpaid-completion rule; receipts; real browser/TV walkthrough; HTTPS + proxy-aware rate limit; `go mod tidy`/CI; Docker run-through; backup + restore test; production config with `SEED_DEV_DATA=false`.
**Before production:** Telebirr integration verified in the approved environment (BLOCKED on credentials/docs/approval); reconciliation/refunds; audit-log UI; monitoring; inventory (if wanted); load/security testing.
