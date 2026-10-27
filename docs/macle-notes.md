# Macle notes: verified vs unverified

**Verified from the original starter:** config files copied unchanged into `apps/customer-miniapp/` (`appID` preserved), `type: "legacy"`, `es5: true`, entry `index.html`.
**Verified from the developer portal page:** toolkit versions 25.9.0 and 26.6.0-LG0001 are offered; a host test app, sign tool, demos, and guides for debugging/packaging exist. The guide pages themselves could not be read (client-rendered).
**Not verified (test in your simulator):**
1. Does the output of `npm run build:macle` (`apps/customer-miniapp/dist/`) open in the simulator? (Relative asset paths via `base: "./"`, ES2018 target.)
2. Does `fetch` to the API work, and which domain whitelist is needed?
3. Does `localStorage` work? (The app falls back silently.)
4. Is `window.ma` present? The app does not use it. `event-bus.js` from the starter is intentionally not included.
5. Does the legacy/es5 setting need a lower build target?

## Added with QR ordering / live updates (all UNVERIFIED in the Macle runtime)
6. How does the Telebirr SuperApp open the Mini-App from a scanned QR, and are URL query parameters (`?t=...`) passed through to the page? The app reads `?t=` from `location.search` or the hash. If the SuperApp uses its own QR/deep-link format, the QR content must change (backend `PUBLIC_MINIAPP_URL`).
7. Is fetch streaming (`ReadableStream`) available? If not, order tracking silently falls back to 20 s polling (code path exists, not tested in Macle).
8. Is `localStorage` persistent across closing/reopening the Mini-App? Table context (4 h) and the last order depend on it.
