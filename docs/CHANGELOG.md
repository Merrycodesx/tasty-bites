# Changelog

## Unreleased: QR table ordering, real-time (SSE), public TV display  (branch `feature/qr-sse-tv`)
Added
- Table QR tokens (`dining_tables.qr_token`, `active`), `GET /api/v1/tables/resolve`, manager endpoints `/api/v1/staff/admin/tables*` (list with QR URLs, add, enable/disable, rotate QR).
- SSE: `events` table written in the same transaction as order changes; `/api/v1/staff/events`, `/api/v1/orders/track/{token}/events`, `/api/v1/display/events` with replay, resync, heartbeat, periodic auth re-check.
- Public TV: `display_devices` (hashed, revocable tokens), `/api/v1/display/orders` snapshot, manager endpoints `/api/v1/staff/admin/displays*` and `/display-settings`, new app `apps/tv-display`.
- Staff app: live updates (polling only as fallback), Setup screen (tables + printable QR sheet, TV pairing, TV settings).
- Customer app: reads/stores the table from the QR (4 h), shows it before checkout, live order tracking with polling fallback.
- `internal/app` (shared routing used by main and tests); `e2e/` end-to-end script; 18 new Go tests, 16 new frontend tests.
- Migration `0002_qr_events_displays.sql` (additive; existing tables get random QR tokens; tested on a database upgraded from 0001 with data).
Changed (contract changes; only the bundled apps consume them)
- **Customer order API:** dine-in now requires `tableToken`; customer `tableId` is rejected (400). Staff orders still use `tableId`.
- **Removed** public `GET /api/v1/tables` (listed table ids). Replaced by `/tables/resolve`.
- Kitchen role no longer receives customer name/phone/delivery address in order payloads (privacy).
- Staff table list returns only active tables.
Fixed
- `PUBLIC_MINIAPP_URL` was not read from the environment (found by the end-to-end test); now covered by a config test.
Rollback: `git checkout main` (tag `baseline-audited` = state before this work). The migration is additive; to undo the schema: drop `events`, `display_devices` and the two `dining_tables` columns (no data in existing tables is changed).

## baseline-audited
First audited state: Go API (auth/RBAC, menu, server-priced orders, idempotency, kitchen flow, cash payment, summary report), customer Mini-App, staff web.
