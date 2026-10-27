# Tasty Bites (placeholder name)

Restaurant ordering platform: Telebirr Mini-App (`apps/customer-miniapp`), staff web POS (`apps/staff-web`), public TV order board (`apps/tv-display`), Go + PostgreSQL API (`backend`).

Status and next steps: `docs/PROJECT_STATUS.md`, `docs/ROADMAP.md`. Real-time and TV design: `docs/realtime-and-tv.md`.

## Run locally
```
cp .env.example .env            # edit passwords
docker compose up -d --build    # PostgreSQL + API on :8080 (SEED_DEV_DATA=true creates sample data and staff accounts)
cd apps/customer-miniapp && npm install && npm run dev      # :5173
cd apps/staff-web && npm install && npm run dev             # :5174
cd apps/tv-display && npm install && npm run dev            # :5175 (pair it from staff Setup, see docs/realtime-and-tv.md)
```
Without Docker: `cd backend && go mod tidy && DATABASE_URL=... SEED_DEV_DATA=true SEED_PASSWORD=... go run ./cmd/api`.
Dev staff usernames: admin, manager, waiter, cashier, kitchen (password = SEED_PASSWORD). Sample data is for development only.

## Test
```
cd backend && TEST_DATABASE_URL='postgres://user:pass@localhost/tastybites_test?sslmode=disable' go test ./...
cd apps/customer-miniapp && npm test && npm run build
cd apps/staff-web && npm test && npm run build
cd apps/tv-display && npm test && npm run build
```

### End-to-end test (disposable database only)
```
cd backend && go build -o /tmp/api ./cmd/api
cd ../e2e && API_BIN=/tmp/api E2E_DATABASE_URL=postgres://user:pass@localhost/tastybites_e2e?sslmode=disable \
  E2E_PSQL="psql -q -t -A -d tastybites_e2e" node --experimental-strip-types e2e.mjs
```
It starts and restarts its own API, seeds dev data, and checks QR → order → kitchen → TV → customer, restart recovery, isolation and revocation. Never point it at real data.
Mini-App package for Macle: `cd apps/customer-miniapp && npm run build:macle` (see docs/macle-notes.md).
