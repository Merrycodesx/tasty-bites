# Decisions and assumptions

- **Name/branding:** "Tasty Bites" is a placeholder. Green `#247a3d`.
- **Money:** integer santim (1 ETB = 100 santim) everywhere; the server computes every price and total.
- **DB driver:** `database/sql` + `lib/pq` (pgx can replace it later). Migrations are embedded SQL run at startup (`schema_migrations`).
- **Auth:** opaque random session tokens (stored hashed, 12 h), bcrypt passwords, `Authorization: Bearer`. The staff app keeps the token in `sessionStorage`: simple and immune to CSRF, but readable by any XSS, so keep the CSP strict and never render unescaped HTML. Move to HttpOnly cookies + CSRF tokens if that tradeoff is unacceptable.
- **Roles:** admin, manager, waiter, cashier, kitchen. Enforced server-side per endpoint and per status transition.
- **Order states:** placed, preparing, ready, served (dine-in only), completed. Cancelling needs a reason, and paid orders cannot be cancelled until refunds exist.
- **Payment policy not fixed:** orders carry a separate `payment_status`; nothing forces pay-before or pay-after. A per-branch setting (`settings` table) is planned, not built.
- **Telebirr:** only the `payments.Provider` interface exists. The default provider returns "not configured". Nothing is simulated.
- **Customer identity:** none yet (anonymous order plus unguessable tracking token). Telebirr auto-login is a later step.
- **Real-time:** REST polling (3-5 s) for now; WebSockets and the outbox are deferred.
- **SQLite/offline:** not used.
