// End-to-end check: QR table -> customer order -> kitchen -> public TV -> customer tracking, plus failure cases.
// Uses the real SSE client code shipped in the frontends. Run: see README ("End-to-end test").
// Env: API_BIN (compiled Go API), E2E_DATABASE_URL (a DISPOSABLE database), E2E_PSQL (command that runs SQL, reads stdin)
import { spawn, execSync } from "node:child_process";
import { createHash } from "node:crypto";
import { connectSse } from "./sse.ts";

const PORT = 8082, B = `http://localhost:${PORT}`;
const results = []; let failed = 0;
const check = (name, ok, detail = "") => { results.push([ok, name, detail]); if (!ok) failed++; console.log(`${ok ? "PASS" : "FAIL"}  ${name}${ok ? "" : "  -> " + detail}`); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const waitFor = async (fn, ms = 8000) => { const end = Date.now() + ms; while (Date.now() < end) { const v = await fn(); if (v) return v; await sleep(100); } return false; };
const sql = (q) => execSync(process.env.E2E_PSQL, { input: q }).toString().trim();

let api;
function startApi() {
  api = spawn(process.env.API_BIN, [], { stdio: "ignore", env: { ...process.env, DATABASE_URL: process.env.E2E_DATABASE_URL, LISTEN_ADDR: `:${PORT}`, SEED_DEV_DATA: "true", SEED_PASSWORD: "e2e-password-1", PUBLIC_MINIAPP_URL: "https://mini.example.test/app", ALLOWED_ORIGINS: "http://localhost:5173" } });
}
async function up() { return waitFor(async () => { try { return (await fetch(`${B}/health`)).ok; } catch { return false; } }, 15000); }

const j = async (method, path, { token, body, idem } = {}) => {
  const res = await fetch(B + path, { method, headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(idem ? { "Idempotency-Key": idem } : {}) }, body: body ? JSON.stringify(body) : undefined });
  let data = null; try { data = await res.json(); } catch {}
  return { status: res.status, data };
};
const login = async (u) => (await j("POST", "/api/v1/auth/login", { body: { username: u, password: "e2e-password-1" } })).data.token;

// A recording client built on the real connectSse (what the TV / kitchen / customer apps use).
function client(path, token) {
  const c = { events: [], ready: 0, fatal: null, state: "connecting" };
  c.close = connectSse({ url: B + path, headers: token ? { Authorization: `Bearer ${token}` } : {}, onReady: () => c.ready++, onEvent: (d) => c.events.push(d), onState: (s) => (c.state = s), onFatal: (s) => (c.fatal = s) });
  return c;
}

startApi();
check("API starts", await up());
const mgr = await login("manager"), kitchen = await login("kitchen");

// ---- 1. QR ----
const tables = (await j("GET", "/api/v1/staff/admin/tables", { token: mgr })).data;
const t1 = tables.tables.find((t) => t.label === "T1");
const qrToken = new URL(t1.qrUrl).searchParams.get("t");
check("manager gets a QR link per table", t1.qrUrl.startsWith("https://mini.example.test/app?t=") && qrToken.length === 64, t1.qrUrl);
const resolved = await j("GET", `/api/v1/tables/resolve?t=${qrToken}`);
check("scanning the QR resolves the right table (no id exposed)", resolved.status === 200 && resolved.data.table.label === "T1" && resolved.data.table.id === undefined);
const bad = await j("GET", `/api/v1/tables/resolve?t=${"0".repeat(64)}`);
check("invalid QR gives a clear error", bad.status === 404 && /not valid/i.test(bad.data.error.message), JSON.stringify(bad.data));

// ---- 2. displays and live clients ----
const disp = (await j("POST", "/api/v1/staff/admin/displays", { token: mgr, body: { name: "E2E TV" } })).data;
const snap = async (tok = disp.token) => j("GET", "/api/v1/display/orders", { token: tok });
const tv = client("/api/v1/display/events", disp.token);
const kc = client("/api/v1/staff/events", kitchen);
check("TV and kitchen streams connect", await waitFor(() => tv.ready > 0 && kc.ready > 0));

// many simultaneous clients
const crowd = Array.from({ length: 15 }, () => client("/api/v1/display/events", disp.token)).concat(Array.from({ length: 15 }, () => client("/api/v1/staff/events", kitchen)));
await waitFor(() => crowd.every((c) => c.ready > 0));

// ---- 3. customer order via QR ----
const key = "e2e-order-key-0001";
const order = { orderType: "dine_in", tableToken: qrToken, items: [{ menuItemId: Number(sql("select id from menu_items where name='Cheese Burger'")), quantity: 1, extraIds: [] }] };
const o1 = await j("POST", "/api/v1/orders", { body: order, idem: key });
check("customer order from QR is created for the right table", o1.status === 201 && o1.data.tableLabel === "T1" && o1.data.status === "placed", JSON.stringify(o1.data));
const o1b = await j("POST", "/api/v1/orders", { body: order, idem: key });
check("duplicate submission returns the same order", o1b.data.id === o1.data.id && sql("select count(*) from orders") === "1");
check("kitchen received it without refreshing", await waitFor(() => kc.events.some((e) => e.type === "order.created" && e.orderNumber === o1.data.orderNumber)));
check("TV received it without refreshing", await waitFor(() => tv.events.some((e) => e.orderNumber === o1.data.orderNumber && e.status === "received")));
check("all 30 simultaneous clients received the event", await waitFor(() => crowd.every((c) => c.events.length >= 1)));
let s = await snap();
const row = s.data.orders.find((r) => r.orderNumber === o1.data.orderNumber);
check("TV snapshot shows only order number + public status", row && Object.keys(row).sort().join() === "orderNumber,status" && row.status === "received", JSON.stringify(row));

// ---- 4. kitchen updates reach customer + TV ----
const cust = client(`/api/v1/orders/track/${o1.data.publicToken}/events`);
await waitFor(() => cust.ready > 0);
const item = (await j("GET", `/api/v1/staff/orders/${o1.data.id}`, { token: kitchen })).data.items[0];
await j("POST", `/api/v1/staff/orders/${o1.data.id}/items/${item.id}/kitchen-status`, { token: kitchen, body: { status: "preparing" } });
check("customer sees 'preparing' live", await waitFor(() => cust.events.some((e) => e.status === "preparing")));
await j("POST", `/api/v1/staff/orders/${o1.data.id}/items/${item.id}/kitchen-status`, { token: kitchen, body: { status: "ready" } });
check("customer sees 'ready' live", await waitFor(() => cust.events.some((e) => e.status === "ready")));
check("TV board shows 'ready' after the event", await waitFor(async () => (await snap()).data.orders.some((r) => r.orderNumber === o1.data.orderNumber && r.status === "ready")));

// ---- 5. restaurant isolation ----
const otherTok = "b".repeat(64);
sql(`insert into branches(name) values('Other Restaurant'); insert into display_devices(branch_id,name,token_hash) values((select max(id) from branches),'other tv','${createHash("sha256").update(otherTok).digest("hex")}');`);
const otherTv = client("/api/v1/display/events", otherTok);
await waitFor(() => otherTv.ready > 0);
const before = otherTv.events.length;
await j("POST", "/api/v1/orders", { body: { orderType: "takeaway", items: order.items }, idem: "e2e-order-key-0002" });
await sleep(800);
check("other restaurant's TV gets no events from this restaurant", otherTv.events.length === before && (await snap(otherTok)).data.orders.length === 0);

// ---- 6. server restart: TV recovers by itself ----
const readyBefore = tv.ready;
api.kill("SIGKILL"); await sleep(1500);
check("TV notices the outage", await waitFor(() => tv.state === "offline", 5000));
startApi(); check("API restarts", await up());
const o3 = await j("POST", "/api/v1/orders", { body: { orderType: "takeaway", items: order.items }, idem: "e2e-order-key-0003" });
check("TV reconnects on its own after the restart", await waitFor(() => tv.ready > readyBefore, 15000));
check("TV board reloads current state after reconnect", await waitFor(async () => (await snap()).data.orders.some((r) => r.orderNumber === o3.data.orderNumber), 8000));
await sleep(500);

// ---- 7. invalid / unauthorized ----
const inv = await j("POST", "/api/v1/orders", { body: { ...order, tableToken: "f".repeat(64) }, idem: "e2e-order-key-0004" });
check("order with an invalid QR is refused clearly", inv.status === 400 && inv.data.error.code === "table_invalid");
check("staff API is not public", (await j("GET", "/api/v1/staff/orders")).status === 401);
check("display token cannot read staff API", (await j("GET", "/api/v1/staff/orders", { token: disp.token })).status === 401);
check("staff token cannot read the display API", (await j("GET", "/api/v1/display/orders", { token: kitchen })).status === 401);

// ---- 8. revoke ----
await j("POST", `/api/v1/staff/admin/displays/${disp.id}/revoke`, { token: mgr });
check("revoked display is refused immediately", (await snap()).status === 401);
check("revoked display stream is closed (within 40 s)", await waitFor(() => tv.fatal === 401, 40000));

[tv, kc, cust, otherTv, ...crowd].forEach((c) => c.close());
api.kill();
console.log(`\n${results.length - failed}/${results.length} checks passed`);
process.exit(failed ? 1 : 0);
