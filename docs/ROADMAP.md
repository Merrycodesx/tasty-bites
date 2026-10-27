# Roadmap (prioritised)

Workflow per stage: one branch, bounded scope, tests + regression, diff review, end-of-stage report (see `PROJECT_STATUS.md` for what is verified).

## Done
- Stage 1–3 (`feature/qr-sse-tv`): table QR ordering, SSE real-time, public TV display, kitchen PII fix.

## Next (pilot blockers), in this order
1. **Owner tests (no code):** Macle simulator (QR launch + `?t=`, fetch to API, streaming, localStorage) and an Android TV browser; send results/screenshots. Decides the QR format and whether polling fallback is needed.
2. **Staff & password management** (admin/manager create staff, roles, deactivate, change password; password policy; session purge). Acceptance: no SQL needed to onboard staff; role tests.
3. **Payment rules & cashier fixes:** configurable dine-in payment timing; block/flag completing unpaid orders; cashier can find unpaid completed orders; receipts. Acceptance: tests for each timing mode; no order lost from the cashier view.
4. **Menu admin completion:** categories, variants, extras, archive; kitchen item/order state consistency.
5. **Deployment hardening:** `go mod tidy` + `go.sum`, CI (vet, tests, builds), reverse-proxy HTTPS example, proxy-aware rate limiting, security headers/CSP, request logging, Docker run-through, backup + restore procedure tested, production env checklist.
6. **Customer order history** and clearer offline/error UX.

## Then
7. Telebirr payment provider (**BLOCKED** until credentials, official docs, approval): `payment_events`, callback verification, reconciliation, refunds.
8. Audit-log UI with old/new values; reports (local calendar day, payment-time based, CSV export).
9. OpenAPI spec; shared API types; frontend component/E2E tests in a browser.
10. Postgres `LISTEN/NOTIFY` for multi-instance SSE (only if more than one API instance is needed).
11. Inventory, recipes, stock movements, low-stock alerts.
