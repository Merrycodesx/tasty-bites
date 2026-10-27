# Tasty Bites (placeholder name)

Restaurant ordering platform: Telebirr Mini-App (`apps/customer-miniapp`), staff web POS (`apps/staff-web`), Go + PostgreSQL API (`backend`).

## Run locally
```
cp .env.example .env            # edit passwords
docker compose up -d --build    # PostgreSQL + API on :8080 (SEED_DEV_DATA=true creates sample data and staff accounts)
cd apps/customer-miniapp && npm install && npm run dev      # :5173
cd apps/staff-web && npm install && npm run dev             # :5174
```
Without Docker: `cd backend && go mod tidy && DATABASE_URL=... SEED_DEV_DATA=true SEED_PASSWORD=... go run ./cmd/api`.
Dev staff usernames: admin, manager, waiter, cashier, kitchen (password = SEED_PASSWORD). Sample data is for development only.

## Test
```
cd backend && TEST_DATABASE_URL='postgres://user:pass@localhost/tastybites_test?sslmode=disable' go test ./...
cd apps/customer-miniapp && npm test && npm run build
cd apps/staff-web && npm test && npm run build
```
Mini-App package for Macle: `cd apps/customer-miniapp && npm run build:macle` (see docs/macle-notes.md).
