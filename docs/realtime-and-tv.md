# Real-time updates and the public TV display

## Choice: Server-Sent Events (SSE), not WebSockets
Every real-time flow here is server → client: kitchen, cashier, customer and TV are *told* something changed and act through normal REST calls. SSE needs no extra infrastructure, works through ordinary proxies, resumes with `Last-Event-ID`, and is simple to authorize (one HTTP request). WebSockets would add a second protocol and two-way state that nothing needs. The clients use `fetch` streaming (not `EventSource`) so they can send an `Authorization` header.

## Event model (API stays the source of truth)
- Every committed order change writes a row in `events` **inside the same database transaction** as the change, then publishes it in memory **after commit**. A rolled-back change records nothing (tested).
- An event only says "order N changed" (+ status). Clients re-read current state from the REST API on every event, on every (re)connect, and on a slow safety poll.
- Streams: `GET /api/v1/staff/events` (staff bearer token, own restaurant), `GET /api/v1/orders/track/{token}/events` (that single order), `GET /api/v1/display/events` (display token). Frames: `ready` (carries latest event id), `order` (id = event id), `resync`, `unauthorized`, `: ping` every 15 s.
- Reconnect: client resends `Last-Event-ID`; the server replays missed events; if more than 500 were missed or the id is unknown (e.g. database reset), it sends `resync` and the client reloads state. Clients back off 1 s → 30 s with jitter and treat 45 s without bytes as a dead connection.
- Auth expiry: sessions/display tokens are re-checked every 30 s; the server sends `unauthorized` and closes. A slow subscriber (buffer full) is dropped with `resync`.
- Events older than 24 h are pruned hourly.
- **Limitation:** the fan-out hub is in-process, so run **one API instance**. Replay works across restarts (events table), but multiple instances would need Postgres `LISTEN/NOTIFY` (see ROADMAP).

## Public TV display (`apps/tv-display`, route `/`)
Runs full-screen in an Android TV browser. No staff credentials, ever.

**Data it may see** (snapshot `GET /api/v1/display/orders` and the stream use the same filter): order number and a public status (`received`, `preparing`, `ready`), plus the table label only if the manager enabled "show table numbers". Never ids, names, phones, notes, totals, payment data or tokens (tested).

**Lifecycle defaults** (manager can change in Setup → TV board settings, stored in `settings`):
- `ready` orders stay for **10 minutes** (1–60) after becoming ready, then leave the board.
- `served`, `completed`, `cancelled` orders leave immediately (collected).
- `received/preparing` orders older than **6 hours** are hidden as stale.
- At most **40** orders returned (10–100); the TV shows 18 tiles per column and "+N more".
- Refresh/reconnect always reloads the snapshot first, then resumes the stream. A 30 s safety poll also expires old ready orders.
- States: not paired, loading, empty ("No active orders"), reconnecting banner, "may be out of date" after 90 s offline, access-revoked screen.

## Pairing, revoking, reconnecting a TV
1. Manager: **Staff app → Setup → Public TV displays → Create pairing link**. The link (`<TV URL>#token=…`) is shown **once**; only a SHA-256 hash is stored.
2. Open the link on the TV browser. The token is read from the URL *fragment* (never sent to a server or logged), saved on the TV, and removed from the address bar.
3. Revoke: **Revoke** next to the display. The next snapshot returns 401 immediately; the open stream closes within 30 s and the TV shows "Display access ended".
4. Re-pair: create a new link (a revoked token never works again). A TV that lost its saved token shows "Display not paired".
5. Display tokens only work on `/api/v1/display/*`; staff tokens do not work there and display tokens do not work on staff routes (tested).

## Table QR codes
Each table has a random 256-bit token (`dining_tables.qr_token`) and an `active` flag. The QR encodes `PUBLIC_MINIAPP_URL?t=<token>`. The customer app calls `GET /api/v1/tables/resolve?t=…` (returns table label + restaurant name only), shows "Your table: T3" prominently, and orders with `tableToken` — customers can **not** send a raw table id. Invalid, disabled, rotated or other-restaurant tokens are refused with a clear message. **Rotate QR** invalidates a printed code instantly; **Disable** stops ordering at that table. The scanned table is remembered on the device for 4 hours so a refresh doesn't lose it.

## Known risks
- Order numbers are sequential (start 1001) and shown on the TV. They grant no access (customer tracking uses a separate random token), but they reveal order volume to anyone who can read the TV.
- A table QR is a bearer secret for ordering at that table; anyone who photographs it can order for that table until it is rotated. Staff should rotate codes if abused.
- Whether the Telebirr SuperApp can open the Mini-App from this QR URL is unverified (docs/macle-notes.md).
