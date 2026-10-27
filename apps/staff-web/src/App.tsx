import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import QRCode from "qrcode";
import { connectSse, SseState } from "./sse";
import { API_BASE, AdminTable, ApiError, Category, DisplayDevice, DisplaySettings, MenuItem, Order, ROLE_SCREENS, Summary, Table, User, api, setToken, setUnauthorizedHandler } from "./api";
import { formatEtb } from "./money";
import { parseEtb } from "./parse";

const LABEL: Record<string, string> = { dashboard: "Dashboard", waiter: "Waiter", kitchen: "Kitchen", cashier: "Cashier", menu: "Menu", setup: "Setup" };
const SESSION_KEY = "tb_staff_session";

function newKey() {
  const c = (globalThis as { crypto?: { randomUUID?: () => string } }).crypto;
  return c?.randomUUID ? c.randomUUID() : `k-${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
}

const Live = createContext<{ tick: number; live: boolean }>({ tick: 0, live: false });

/** Loads data, reloads whenever a live event arrives, and falls back to polling when the stream is not live. */
function usePoll<T>(load: () => Promise<T>, ms = 4000) {
  const { tick, live } = useContext(Live);
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const ref = useRef(load); ref.current = load;
  const refresh = useCallback(async () => {
    try { setData(await ref.current()); setError(null); } catch (e) { setError((e as ApiError).message); }
  }, []);
  useEffect(() => { refresh(); }, [refresh, tick]);
  useEffect(() => { const id = setInterval(refresh, live ? 60000 : ms); return () => clearInterval(id); }, [refresh, live, ms]);
  return { data, error, refresh };
}

export default function App() {
  const [user, setUser] = useState<User | null>(null);
  const [screen, setScreen] = useState("");
  const [tick, setTick] = useState(0);
  const [conn, setConn] = useState<SseState>("connecting");

  const signOut = useCallback(() => { setToken(null); setUser(null); try { sessionStorage.removeItem(SESSION_KEY); } catch { /* ignore */ } }, []);
  useEffect(() => { setUnauthorizedHandler(signOut); }, [signOut]);
  // One live stream per signed-in session. Events only say "something changed"; each screen re-reads the API.
  const sessionToken = useRef<string | null>(null);
  useEffect(() => {
    if (!user) return;
    let t: ReturnType<typeof setTimeout> | undefined;
    const bump = () => { clearTimeout(t); t = setTimeout(() => setTick((n) => n + 1), 150); };
    const close = connectSse({
      url: `${API_BASE}/api/v1/staff/events`, headers: { Authorization: `Bearer ${sessionToken.current ?? ""}` },
      onReady: bump, onEvent: bump, onState: setConn, onFatal: (st) => { if (st === 401) signOut(); },
    });
    return () => { clearTimeout(t); close(); };
  }, [user, signOut]);
  // Session token lives in sessionStorage (cleared when the tab closes). Tradeoff documented in docs/decisions.md.
  useEffect(() => {
    try {
      const raw = sessionStorage.getItem(SESSION_KEY);
      if (raw) { const s = JSON.parse(raw) as { token: string; user: User }; setToken(s.token); sessionToken.current = s.token; setUser(s.user); setScreen(ROLE_SCREENS[s.user.role][0]); }
    } catch { /* ignore */ }
  }, []);

  if (!user) return <Login onDone={(t, u) => { setToken(t); sessionToken.current = t; setUser(u); setScreen(ROLE_SCREENS[u.role][0]);
    try { sessionStorage.setItem(SESSION_KEY, JSON.stringify({ token: t, user: u })); } catch { /* ignore */ } }} />;

  const screens = ROLE_SCREENS[user.role];
  return (
    <Live.Provider value={{ tick, live: conn === "live" }}>
    <div className="shell">
      <nav className="side" aria-label="Main">
        <h1>Tasty Bites</h1>
        {screens.map((s) => <button key={s} className={screen === s ? "on" : ""} onClick={() => setScreen(s)}>{LABEL[s]}</button>)}
        <div className="who"><span title="Live updates">{conn === "live" ? "● Live" : conn === "unsupported" ? "Live updates unavailable (polling)" : "○ Reconnecting… (polling)"}</span><br />{user.fullName} · {user.role}<br /><button onClick={() => { api.logout().catch(() => {}); signOut(); }}>Sign out</button></div>
      </nav>
      <main>
        {screen === "dashboard" && <Dashboard />}
        {screen === "waiter" && <Waiter role={user.role} />}
        {screen === "kitchen" && <Kitchen />}
        {screen === "cashier" && <Cashier />}
        {screen === "menu" && <MenuAdmin />}
        {screen === "setup" && <Setup />}
      </main>
    </div>
    </Live.Provider>
  );
}

function Login(p: { onDone: (t: string, u: User) => void }) {
  const [u, setU] = useState(""); const [pw, setPw] = useState(""); const [err, setErr] = useState(""); const [busy, setBusy] = useState(false);
  async function go(e: React.FormEvent) {
    e.preventDefault(); setBusy(true); setErr("");
    try { const r = await api.login(u, pw); p.onDone(r.token, r.user); } catch (x) { setErr((x as ApiError).message); } finally { setBusy(false); }
  }
  return (
    <form className="login" onSubmit={go}>
      <h1>Tasty Bites staff</h1>
      {err && <div className="error" role="alert">{err}</div>}
      <label className="f">Username<input autoFocus autoComplete="username" value={u} onChange={(e) => setU(e.target.value)} /></label>
      <label className="f">Password<input type="password" autoComplete="current-password" value={pw} onChange={(e) => setPw(e.target.value)} /></label>
      <button className="btn primary" disabled={busy || !u || !pw}>{busy ? "Signing in…" : "Sign in"}</button>
    </form>
  );
}

const ErrorBar = ({ msg }: { msg: string | null }) => (msg ? <div className="error" role="alert">{msg}</div> : null);
const statusPill = (s: string) => <span className={s === "cancelled" ? "pill bad" : s === "placed" ? "pill warn" : "pill"}>{s}</span>;

function Dashboard() {
  const [days, setDays] = useState(1);
  const sum = usePoll<Summary>(() => api.summary(days), 10000);
  const act = usePoll(() => api.orders(), 5000);
  return (
    <>
      <h2>Dashboard</h2>
      <ErrorBar msg={sum.error} />
      <div className="row" style={{ marginBottom: 12 }}>
        {[1, 7, 30].map((d) => <button key={d} className={d === days ? "btn primary" : "btn"} onClick={() => setDays(d)}>{d === 1 ? "Today (24h)" : `Last ${d} days`}</button>)}
      </div>
      {!sum.data ? <div className="empty">Loading…</div> : (
        <div className="grid">
          <div className="card"><span className="muted">Paid sales</span><span className="stat">{formatEtb(sum.data.paidSalesSantim)}</span></div>
          <div className="card"><span className="muted">Orders (not cancelled)</span><span className="stat">{sum.data.orderCount}</span></div>
          <div className="card"><span className="muted">Average paid order</span><span className="stat">{formatEtb(sum.data.averagePaidOrderSantim)}</span></div>
          <div className="card"><span className="muted">Open orders now</span><span className="stat">{act.data?.orders.length ?? "…"}</span></div>
        </div>
      )}
      <h2 style={{ marginTop: 24 }}>Top items</h2>
      {sum.data && sum.data.topItems.length === 0 ? <div className="empty">No orders in this period yet.</div> : (
        <table><thead><tr><th>Item</th><th>Qty</th><th>Revenue</th></tr></thead>
          <tbody>{sum.data?.topItems.map((t) => <tr key={t.name}><td>{t.name}</td><td>{t.quantity}</td><td>{formatEtb(t.revenueSantim)}</td></tr>)}</tbody></table>
      )}
    </>
  );
}

interface Line { item: MenuItem; variantId?: number; extraIds: number[]; quantity: number; note: string }
function lineTotal(l: Line) {
  const base = l.variantId ? l.item.variants.find((v) => v.id === l.variantId)?.priceSantim ?? 0 : l.item.priceSantim;
  return (base + l.extraIds.reduce((s, id) => s + (l.item.extras.find((e) => e.id === id)?.priceSantim ?? 0), 0)) * l.quantity;
}

function Waiter({ role }: { role: string }) {
  const menu = usePoll(() => api.menu(), 30000);
  const tables = usePoll(() => api.tables(), 5000);
  const orders = usePoll(() => api.orders(), 4000);
  const [tableId, setTableId] = useState<number | undefined>();
  const [type, setType] = useState<"dine_in" | "takeaway">("dine_in");
  const [lines, setLines] = useState<Line[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const key = useRef(newKey());
  const canCreate = role !== "kitchen";

  function addItem(item: MenuItem) {
    setLines((ls) => [...ls, { item, variantId: item.variants[0]?.id, extraIds: [], quantity: 1, note: "" }]);
  }
  const upd = (i: number, patch: Partial<Line>) => setLines((ls) => ls.map((l, j) => (j === i ? { ...l, ...patch } : l)));
  async function send() {
    setBusy(true); setErr(null);
    try {
      await api.createOrder({
        orderType: type, tableId: type === "dine_in" ? tableId : undefined, customerName: "", customerPhone: "", deliveryAddress: "", notes: "",
        items: lines.map((l) => ({ menuItemId: l.item.id, variantId: l.variantId, extraIds: l.extraIds, quantity: l.quantity, note: l.note })),
      }, key.current);
      setLines([]); key.current = newKey(); orders.refresh(); tables.refresh();
    } catch (e) { setErr((e as ApiError).message); } finally { setBusy(false); }
  }
  async function move(o: Order, status: string) {
    let reason = "";
    if (status === "cancelled") { reason = window.prompt("Reason for cancelling this order?") ?? ""; if (!reason.trim()) return; }
    try { await api.setStatus(o.id, status, reason); orders.refresh(); tables.refresh(); } catch (e) { setErr((e as ApiError).message); }
  }
  const total = lines.reduce((s, l) => s + lineTotal(l), 0);

  return (
    <>
      <h2>Waiter</h2>
      <ErrorBar msg={err ?? menu.error ?? orders.error} />
      <div className="cols">
        {canCreate && (
          <section>
            <div className="row" style={{ marginBottom: 10 }}>
              <button className={type === "dine_in" ? "btn primary" : "btn"} onClick={() => setType("dine_in")}>Dine in</button>
              <button className={type === "takeaway" ? "btn primary" : "btn"} onClick={() => setType("takeaway")}>Takeaway</button>
            </div>
            {type === "dine_in" && (
              <div className="tables" style={{ marginBottom: 12 }} role="radiogroup" aria-label="Table">
                {tables.data?.tables.map((t: Table) => (
                  <button key={t.id} role="radio" aria-checked={tableId === t.id} className={`tbl ${t.occupied ? "occ" : ""} ${tableId === t.id ? "sel" : ""}`} onClick={() => setTableId(t.id)}>
                    {t.label}<div className="muted">{t.occupied ? "Occupied" : "Free"}</div>
                  </button>
                ))}
              </div>
            )}
            {lines.length > 0 && (
              <div className="card" style={{ marginBottom: 12 }}>
                {lines.map((l, i) => (
                  <div key={i} className="kcol">
                    <div className="line"><strong>{l.item.name}</strong><span>{formatEtb(lineTotal(l))}</span></div>
                    {l.item.variants.length > 0 && (
                      <select value={l.variantId} onChange={(e) => upd(i, { variantId: Number(e.target.value) })}>{l.item.variants.map((v) => <option key={v.id} value={v.id}>{v.name}</option>)}</select>
                    )}
                    {l.item.extras.map((x) => (
                      <label key={x.id} className="muted"><input type="checkbox" checked={l.extraIds.includes(x.id)} onChange={(e) => upd(i, { extraIds: e.target.checked ? [...l.extraIds, x.id] : l.extraIds.filter((z) => z !== x.id) })} /> {x.name} (+{formatEtb(x.priceSantim)})</label>
                    ))}
                    <div className="row">
                      <input type="number" min={1} max={50} value={l.quantity} style={{ width: 70 }} onChange={(e) => upd(i, { quantity: Math.max(1, Math.min(50, Number(e.target.value) || 1)) })} aria-label="Quantity" />
                      <input placeholder="Note" value={l.note} maxLength={200} onChange={(e) => upd(i, { note: e.target.value })} />
                      <button className="btn danger" onClick={() => setLines(lines.filter((_, j) => j !== i))}>Remove</button>
                    </div>
                  </div>
                ))}
                <div className="line"><strong>Estimated total</strong><strong>{formatEtb(total)}</strong></div>
                <button className="btn primary" disabled={busy || (type === "dine_in" && !tableId)} onClick={send}>{busy ? "Sending…" : "Send to kitchen"}</button>
              </div>
            )}
            <h3>Menu</h3>
            {!menu.data ? <div className="empty">Loading menu…</div> : menu.data.categories.map((c: Category) => (
              <div key={c.id} style={{ marginBottom: 10 }}>
                <div className="muted">{c.name}</div>
                <div className="row">{c.items.map((i) => <button key={i.id} className="btn" disabled={!i.available} title={i.available ? "" : "Unavailable"} onClick={() => addItem(i)}>{i.name}</button>)}</div>
              </div>
            ))}
          </section>
        )}
        <section>
          <h3>Open orders</h3>
          {orders.data && orders.data.orders.length === 0 && <div className="empty">No open orders.</div>}
          <div className="kcol">
            {orders.data?.orders.map((o) => (
              <div key={o.id} className="card">
                <h3>#{o.orderNumber} {statusPill(o.status)}</h3>
                <div className="muted">{o.orderType.replace("_", " ")}{o.tableLabel ? ` · ${o.tableLabel}` : ""} · {o.source} · {formatEtb(o.totalSantim)} · {o.paymentStatus}</div>
                {o.items.map((i) => <div key={i.id} className="line"><span>{i.quantity} × {i.name}{i.variantName ? ` (${i.variantName})` : ""}</span><span className="muted">{i.kitchenStatus}</span></div>)}
                <div className="row">
                  {o.status === "ready" && o.orderType === "dine_in" && <button className="btn primary" onClick={() => move(o, "served")}>Mark served</button>}
                  {o.status === "ready" && o.orderType !== "dine_in" && <button className="btn primary" onClick={() => move(o, "completed")}>Hand over</button>}
                  {o.status === "served" && o.paymentStatus === "paid" && <button className="btn primary" onClick={() => move(o, "completed")}>Close table</button>}
                  {(o.status === "placed" || ["manager", "admin"].includes(role)) && o.status !== "served" && <button className="btn danger" onClick={() => move(o, "cancelled")}>Cancel</button>}
                </div>
              </div>
            ))}
          </div>
        </section>
      </div>
    </>
  );
}

function Kitchen() {
  const orders = usePoll(() => api.orders(), 3000);
  const [err, setErr] = useState<string | null>(null);
  async function item(o: Order, itemId: number, status: string) {
    try { await api.kitchenItem(o.id, itemId, status); orders.refresh(); } catch (e) { setErr((e as ApiError).message); }
  }
  const cols: [string, string[]][] = [["New", ["placed"]], ["Preparing", ["preparing"]], ["Ready for pickup", ["ready"]]];
  return (
    <>
      <h2>Kitchen</h2>
      <ErrorBar msg={err ?? orders.error} />
      <div className="board">
        {cols.map(([title, sts]) => {
          const list = orders.data?.orders.filter((o) => sts.includes(o.status)) ?? [];
          return (
            <section key={title}>
              <h3>{title} ({list.length})</h3>
              <div className="kcol">
                {list.length === 0 && <div className="empty">Nothing here.</div>}
                {list.map((o) => (
                  <div key={o.id} className="card">
                    <h3>#{o.orderNumber}<span className="muted">{o.orderType.replace("_", " ")}{o.tableLabel ? ` · ${o.tableLabel}` : ""}</span></h3>
                    {o.notes && <div className="muted">Order note: {o.notes}</div>}
                    {o.items.map((i) => (
                      <div key={i.id} className="line">
                        <span><strong>{i.quantity} × {i.name}</strong>{i.variantName ? ` (${i.variantName})` : ""}
                          {i.options.length > 0 && <div className="muted">{i.options.map((x) => x.name).join(", ")}</div>}
                          {i.note && <div className="muted">Note: {i.note}</div>}</span>
                        <span className="row">
                          {i.kitchenStatus === "queued" && <button className="btn" onClick={() => item(o, i.id, "preparing")}>Start</button>}
                          {i.kitchenStatus === "preparing" && <button className="btn primary" onClick={() => item(o, i.id, "ready")}>Ready</button>}
                          {i.kitchenStatus === "ready" && <span className="pill">ready</span>}
                        </span>
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            </section>
          );
        })}
      </div>
    </>
  );
}

function Cashier() {
  const orders = usePoll(() => api.orders(), 4000);
  const [err, setErr] = useState<string | null>(null);
  async function pay(o: Order) {
    if (!window.confirm(`Record cash payment of ${formatEtb(o.totalSantim)} for order #${o.orderNumber}?`)) return;
    try { await api.payCash(o.id); orders.refresh(); } catch (e) { setErr((e as ApiError).message); }
  }
  async function complete(o: Order) {
    try { await api.setStatus(o.id, "completed"); orders.refresh(); } catch (e) { setErr((e as ApiError).message); }
  }
  const open = orders.data?.orders ?? [];
  return (
    <>
      <h2>Cashier</h2>
      <ErrorBar msg={err ?? orders.error} />
      {orders.data && open.length === 0 && <div className="empty">No open bills.</div>}
      <div className="grid">
        {open.map((o) => (
          <div key={o.id} className="card">
            <h3>#{o.orderNumber} {statusPill(o.status)}</h3>
            <div className="muted">{o.orderType.replace("_", " ")}{o.tableLabel ? ` · ${o.tableLabel}` : ""}</div>
            {o.items.map((i) => <div key={i.id} className="line"><span>{i.quantity} × {i.name}</span><span>{formatEtb(i.lineTotalSantim)}</span></div>)}
            <div className="line"><strong>Total</strong><strong>{formatEtb(o.totalSantim)}</strong></div>
            <div className="row">
              <span className={o.paymentStatus === "paid" ? "pill" : "pill warn"}>{o.paymentStatus}</span>
              {o.paymentStatus !== "paid" && <button className="btn primary" onClick={() => pay(o)}>Take cash</button>}
              {o.paymentStatus === "paid" && ["ready", "served"].includes(o.status) && <button className="btn" onClick={() => complete(o)}>Complete order</button>}
            </div>
            {o.paymentStatus !== "paid" && <div className="muted">Telebirr payment is not enabled yet.</div>}
          </div>
        ))}
      </div>
    </>
  );
}

function MenuAdmin() {
  const menu = usePoll(() => api.menu(), 15000);
  const [err, setErr] = useState<string | null>(null);
  const [form, setForm] = useState({ categoryId: 0, name: "", description: "", price: "" });
  async function toggle(i: MenuItem) { try { await api.patchItem(i.id, { available: !i.available }); menu.refresh(); } catch (e) { setErr((e as ApiError).message); } }
  async function price(i: MenuItem) {
    const v = window.prompt(`New price for ${i.name} (ETB)`, String(i.priceSantim / 100)); if (v === null) return;
    const s = parseEtb(v); if (s === null) { setErr("Enter a valid price like 120 or 120.50."); return; }
    try { await api.patchItem(i.id, { priceSantim: s }); menu.refresh(); } catch (e) { setErr((e as ApiError).message); }
  }
  async function add(e: React.FormEvent) {
    e.preventDefault(); const s = parseEtb(form.price);
    if (s === null || !form.categoryId) { setErr("Choose a category and enter a valid price."); return; }
    try { await api.createItem({ categoryId: form.categoryId, name: form.name, description: form.description, priceSantim: s }); setForm({ ...form, name: "", description: "", price: "" }); setErr(null); menu.refresh(); } catch (x) { setErr((x as ApiError).message); }
  }
  return (
    <>
      <h2>Menu</h2>
      <ErrorBar msg={err ?? menu.error} />
      <form className="card" onSubmit={add} style={{ marginBottom: 16 }}>
        <h3>Add item</h3>
        <div className="row">
          <select value={form.categoryId} onChange={(e) => setForm({ ...form, categoryId: Number(e.target.value) })} aria-label="Category">
            <option value={0}>Category</option>{menu.data?.categories.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
          <input placeholder="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
          <input placeholder="Description" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
          <input placeholder="Price (ETB)" inputMode="decimal" value={form.price} onChange={(e) => setForm({ ...form, price: e.target.value })} style={{ width: 120 }} required />
          <button className="btn primary">Add item</button>
        </div>
      </form>
      {menu.data?.categories.map((c) => (
        <section key={c.id} style={{ marginBottom: 16 }}>
          <h3>{c.name}</h3>
          <table><thead><tr><th>Item</th><th>Price</th><th>Status</th><th></th></tr></thead><tbody>
            {c.items.map((i) => (
              <tr key={i.id}><td>{i.name}</td><td>{formatEtb(i.priceSantim)}</td>
                <td><span className={i.available ? "pill" : "pill bad"}>{i.available ? "Available" : "Unavailable"}</span></td>
                <td className="row"><button className="btn" onClick={() => price(i)}>Change price</button><button className="btn" onClick={() => toggle(i)}>{i.available ? "Mark unavailable" : "Mark available"}</button></td></tr>
            ))}
          </tbody></table>
        </section>
      ))}
    </>
  );
}


function Qr({ text, size = 160 }: { text: string; size?: number }) {
  const [src, setSrc] = useState("");
  useEffect(() => { let on = true; QRCode.toDataURL(text, { width: size, margin: 1, errorCorrectionLevel: "M" }).then((u) => on && setSrc(u)).catch(() => on && setSrc("")); return () => { on = false; }; }, [text, size]);
  return src ? <img src={src} width={size} height={size} alt="QR code" /> : <div className="muted" style={{ width: size }}>QR unavailable</div>;
}

const TV_URL = (import.meta.env.VITE_TV_URL as string | undefined) ?? "http://localhost:5175";

function Setup() {
  const tables = usePoll(() => api.adminTables(), 60000);
  const displays = usePoll(() => api.displays(), 60000);
  const [settings, setSettings] = useState<DisplaySettings | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [newTable, setNewTable] = useState({ label: "", seats: "4" });
  const [newDisplay, setNewDisplay] = useState("");
  const [pairing, setPairing] = useState<{ name: string; link: string } | null>(null);
  useEffect(() => { api.displaySettings().then(setSettings).catch((e) => setErr((e as ApiError).message)); }, []);
  const run = async (f: () => Promise<unknown>, ok?: string) => { try { await f(); setErr(null); if (ok) setMsg(ok); tables.refresh(); displays.refresh(); } catch (e) { setErr((e as ApiError).message); } };

  return (
    <>
      <h2>Setup</h2>
      <ErrorBar msg={err ?? tables.error ?? displays.error} />
      {msg && <div className="pill" style={{ marginBottom: 12 }}>{msg}</div>}

      <section className="noprint">
        <h3>Public TV displays</h3>
        <p className="muted">Each TV gets its own pairing link. Open the link once on the TV; it is stored there. Revoke a display to stop it immediately.</p>
        <form className="row" onSubmit={(e) => { e.preventDefault(); run(async () => { const d = await api.createDisplay(newDisplay); setPairing({ name: d.name, link: `${TV_URL}#token=${d.token}` }); setNewDisplay(""); }); }}>
          <input placeholder="Display name (e.g. Entrance TV)" value={newDisplay} onChange={(e) => setNewDisplay(e.target.value)} required maxLength={60} />
          <button className="btn primary">Create pairing link</button>
        </form>
        {pairing && (
          <div className="card" style={{ marginTop: 10 }}>
            <strong>Pairing link for “{pairing.name}” — shown only once</strong>
            <input readOnly value={pairing.link} onFocus={(e) => e.target.select()} aria-label="Pairing link" />
            <div className="row"><button className="btn" onClick={() => navigator.clipboard?.writeText(pairing.link)}>Copy link</button><button className="btn" onClick={() => setPairing(null)}>I have saved it</button></div>
            <Qr text={pairing.link} size={180} />
          </div>
        )}
        <table style={{ marginTop: 10 }}><thead><tr><th>Name</th><th>Status</th><th>Last seen</th><th></th></tr></thead><tbody>
          {displays.data?.displays.map((d: DisplayDevice) => (
            <tr key={d.id}><td>{d.name}</td><td><span className={d.revokedAt ? "pill bad" : "pill"}>{d.revokedAt ? "Revoked" : "Active"}</span></td>
              <td>{d.lastSeenAt ? new Date(d.lastSeenAt).toLocaleString() : "Never"}</td>
              <td>{!d.revokedAt && <button className="btn danger" onClick={() => window.confirm(`Revoke “${d.name}”? The TV stops showing orders immediately.`) && run(() => api.revokeDisplay(d.id), "Display revoked.")}>Revoke</button>}</td></tr>
          ))}
        </tbody></table>
        {displays.data && displays.data.displays.length === 0 && <div className="empty">No displays yet.</div>}
        {settings && (
          <form className="card" style={{ marginTop: 12 }} onSubmit={(e) => { e.preventDefault(); run(async () => setSettings(await api.saveDisplaySettings(settings)), "TV settings saved."); }}>
            <h3>TV board settings</h3>
            <label className="f">Keep “ready” orders on screen for (minutes)<input type="number" min={1} max={60} value={settings.readyMinutes} onChange={(e) => setSettings({ ...settings, readyMinutes: Number(e.target.value) })} /></label>
            <label className="f">Maximum orders shown<input type="number" min={10} max={100} value={settings.maxOrders} onChange={(e) => setSettings({ ...settings, maxOrders: Number(e.target.value) })} /></label>
            <label><input type="checkbox" checked={settings.showTable} onChange={(e) => setSettings({ ...settings, showTable: e.target.checked })} /> Show table numbers on the public TV</label>
            <button className="btn primary">Save</button>
          </form>
        )}
      </section>

      <section style={{ marginTop: 24 }}>
        <div className="row noprint"><h3 style={{ margin: 0 }}>Tables and QR codes</h3><button className="btn" onClick={() => window.print()}>Print QR sheet</button></div>
        {tables.data && !tables.data.qrConfigured && <div className="error noprint">The server has no PUBLIC_MINIAPP_URL, so QR links cannot be built. Set it and restart the API.</div>}
        <form className="row noprint" style={{ margin: "10px 0" }} onSubmit={(e) => { e.preventDefault(); run(async () => { await api.createTable(newTable.label, Number(newTable.seats) || 4); setNewTable({ label: "", seats: "4" }); }, "Table added."); }}>
          <input placeholder="Table label (e.g. T9)" value={newTable.label} onChange={(e) => setNewTable({ ...newTable, label: e.target.value })} required maxLength={20} />
          <input type="number" min={1} max={50} value={newTable.seats} onChange={(e) => setNewTable({ ...newTable, seats: e.target.value })} style={{ width: 80 }} aria-label="Seats" />
          <button className="btn primary">Add table</button>
        </form>
        <div className="grid qrsheet">
          {tables.data?.tables.map((t: AdminTable) => (
            <div key={t.id} className="card" style={{ opacity: t.active ? 1 : 0.55 }}>
              <h3>Table {t.label}<span className={t.active ? "pill" : "pill bad"}>{t.active ? "Active" : "Disabled"}</span></h3>
              {t.qrUrl ? <Qr text={t.qrUrl} /> : <div className="muted">No QR (server URL not set)</div>}
              <div className="muted">Scan to order at table {t.label}</div>
              <div className="row noprint">
                <button className="btn" onClick={() => run(() => api.patchTable(t.id, { active: !t.active }))}>{t.active ? "Disable" : "Enable"}</button>
                <button className="btn danger" onClick={() => window.confirm(`Rotate the QR code for table ${t.label}? Printed codes stop working.`) && run(() => api.rotateQr(t.id), "QR rotated. Print the new code.")}>Rotate QR</button>
                {t.qrUrl && <button className="btn" onClick={() => navigator.clipboard?.writeText(t.qrUrl)}>Copy link</button>}
              </div>
            </div>
          ))}
        </div>
      </section>
    </>
  );
}
