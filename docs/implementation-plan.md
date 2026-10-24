# Implementation plan

- [x] Backend: schema + migrations, auth/RBAC, menu, server-side priced orders, idempotency, state machine, kitchen, cash payment, summary report, provider interface
- [x] Customer Mini-App: menu, search, categories, variants/extras, cart, checkout, order tracking, loading/empty/error states
- [x] Staff web: login, waiter, kitchen, cashier, menu admin, dashboard
- [ ] Verify Mini-App build in the Macle simulator (you)
- [ ] OpenAPI spec file; WebSocket events + outbox
- [ ] Telebirr provider (needs credentials + official docs)
- [ ] Per-branch payment-timing setting; staff/role admin UI; audit log UI
- [ ] Inventory, recipes, stock movements; fuller reports/exports
- [ ] Backup/restore tests, CI, production config
