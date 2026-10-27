import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { API_BASE, ApiError, Category, MenuItem, Order, OrderRequest, getMenu, placeOrder, resolveTable, trackOrder } from "./api";
import { connectSse } from "./sse";
import { formatEtb } from "./money";

interface CartLine { key: string; item: MenuItem; variantId?: number; extraIds: number[]; quantity: number; note: string }
type Screen = "menu" | "cart" | "checkout" | "status";

const TOKEN_KEY = "tb_last_order_token";
const TABLE_KEY = "tb_table";
const TABLE_TTL_MS = 4 * 60 * 60 * 1000; // a scanned table stays valid on this device for 4 hours

interface TableCtx { state: "none" | "loading" | "ok" | "invalid" | "error"; token?: string; label?: string; message?: string }

function readTableToken(): string | null {
  try {
    const fromUrl = new URLSearchParams(window.location.search).get("t") ?? new URLSearchParams(window.location.hash.replace(/^#\/?\??/, "")).get("t");
    if (fromUrl) { localStorage.setItem(TABLE_KEY, JSON.stringify({ token: fromUrl, savedAt: Date.now() })); return fromUrl; }
    const raw = localStorage.getItem(TABLE_KEY);
    if (raw) {
      const v = JSON.parse(raw) as { token: string; savedAt: number };
      if (Date.now() - v.savedAt < TABLE_TTL_MS) return v.token;
      localStorage.removeItem(TABLE_KEY);
    }
  } catch { /* storage or URL APIs may be unavailable in the Mini-App runtime */ }
  return null;
}
function forgetTable() { try { localStorage.removeItem(TABLE_KEY); } catch { /* ignore */ } }
const store = {
  get: () => { try { return localStorage.getItem(TOKEN_KEY); } catch { return null; } },
  set: (v: string) => { try { localStorage.setItem(TOKEN_KEY, v); } catch { /* storage may be unavailable in the Mini-App runtime */ } },
};

function newKey(): string {
  const c = (globalThis as { crypto?: { randomUUID?: () => string } }).crypto;
  return c?.randomUUID ? c.randomUUID() : `k-${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
}

function linePrice(l: CartLine): number {
  const base = l.variantId ? l.item.variants.find((v) => v.id === l.variantId)?.priceSantim ?? l.item.priceSantim : l.item.priceSantim;
  const extras = l.extraIds.reduce((s, id) => s + (l.item.extras.find((e) => e.id === id)?.priceSantim ?? 0), 0);
  return (base + extras) * l.quantity;
}

const STATUS_TEXT: Record<string, string> = {
  placed: "Order received", preparing: "Being prepared", ready: "Ready", served: "Served", completed: "Completed", cancelled: "Cancelled",
};
const PAY_TEXT: Record<string, string> = {
  unpaid: "Not paid yet", pending: "Payment pending", paid: "Paid", failed: "Payment failed", cancelled: "Payment cancelled", refunded: "Refunded",
};

export default function App() {
  const [screen, setScreen] = useState<Screen>("menu");
  const [menu, setMenu] = useState<Category[] | null>(null);
  const [menuError, setMenuError] = useState<string | null>(null);
  const [cart, setCart] = useState<CartLine[]>([]);
  const [token, setToken] = useState<string | null>(store.get());
  const [picked, setPicked] = useState<MenuItem | null>(null);
  const [table, setTable] = useState<TableCtx>({ state: "none" });

  const loadTable = useCallback(() => {
    const token = readTableToken();
    if (!token) { setTable({ state: "none" }); return; }
    setTable({ state: "loading", token });
    resolveTable(token)
      .then((r) => setTable({ state: "ok", token, label: r.table.label }))
      .catch((e: ApiError) => {
        if (e.status === 404) { forgetTable(); setTable({ state: "invalid", message: e.message }); }
        else setTable({ state: "error", token, message: e.message });
      });
  }, []);
  useEffect(loadTable, [loadTable]);

  const loadMenu = useCallback(() => {
    setMenu(null); setMenuError(null);
    getMenu().then((m) => setMenu(m.categories)).catch((e: ApiError) => setMenuError(e.message));
  }, []);
  useEffect(loadMenu, [loadMenu]);

  const count = cart.reduce((s, l) => s + l.quantity, 0);
  const estimate = cart.reduce((s, l) => s + linePrice(l), 0);

  return (
    <div className="app">
      {screen === "menu" && (
        <MenuScreen menu={menu} error={menuError} retry={loadMenu} onPick={setPicked} hasOrder={!!token} table={table} retryTable={loadTable}
          onTrack={() => setScreen("status")} />
      )}
      {screen === "cart" && (
        <CartScreen cart={cart} setCart={setCart} estimate={estimate} back={() => setScreen("menu")} next={() => setScreen("checkout")} />
      )}
      {screen === "checkout" && (
        <CheckoutScreen cart={cart} estimate={estimate} table={table} retryTable={loadTable} back={() => setScreen("cart")}
          done={(o) => { if (o.publicToken) { store.set(o.publicToken); setToken(o.publicToken); } setCart([]); setScreen("status"); }} />
      )}
      {screen === "status" && token && <StatusScreen token={token} back={() => setScreen("menu")} />}
      {screen === "menu" && count > 0 && (
        <button className="cartbar" onClick={() => setScreen("cart")}>
          <span>{count} {count === 1 ? "item" : "items"}</span>
          <span>View cart · {formatEtb(estimate)}</span>
        </button>
      )}
      {picked && (
        <ItemSheet item={picked} close={() => setPicked(null)}
          add={(line) => { setCart((c) => mergeLine(c, line)); setPicked(null); }} />
      )}
    </div>
  );
}

function mergeLine(cart: CartLine[], line: CartLine): CartLine[] {
  const i = cart.findIndex((l) => l.key === line.key);
  if (i < 0) return [...cart, line];
  const copy = cart.slice();
  copy[i] = { ...copy[i], quantity: Math.min(50, copy[i].quantity + line.quantity) };
  return copy;
}

function TableNotice({ table, retry }: { table: TableCtx; retry: () => void }) {
  if (table.state === "invalid") return <div className="error" role="alert">{table.message} You can still order takeaway.</div>;
  if (table.state === "error") return <div className="error" role="alert">{table.message} <button className="link" onClick={retry}>Try again</button></div>;
  return null;
}

function MenuScreen(p: { menu: Category[] | null; error: string | null; retry: () => void; onPick: (i: MenuItem) => void; hasOrder: boolean; onTrack: () => void; table: TableCtx; retryTable: () => void }) {
  const [q, setQ] = useState("");
  const [cat, setCat] = useState<number | null>(null);
  const items = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (p.menu ?? []).filter((c) => cat === null || c.id === cat).flatMap((c) => c.items)
      .filter((i) => !needle || i.name.toLowerCase().includes(needle) || i.description.toLowerCase().includes(needle));
  }, [p.menu, q, cat]);

  return (
    <>
      <header className="hero">
        <h1>Tasty Bites</h1>
        {p.table.state === "ok" && <div className="tablebadge" aria-live="polite">Your table: <strong>{p.table.label}</strong></div>}
        {p.table.state === "loading" && <div className="tablebadge">Checking your table…</div>}
        <input className="search" type="search" placeholder="Search the menu" aria-label="Search the menu" value={q} onChange={(e) => setQ(e.target.value)} />
      </header>
      <TableNotice table={p.table} retry={p.retryTable} />
      {p.hasOrder && <button className="banner" onClick={p.onTrack}>Track your latest order</button>}
      {p.error && <div className="state" role="alert"><p>{p.error}</p><button className="primary" onClick={p.retry}>Try again</button></div>}
      {!p.menu && !p.error && <div className="state" aria-busy="true">Loading menu…</div>}
      {p.menu && (
        <>
          <div className="chips" role="tablist">
            <button className={cat === null ? "chip on" : "chip"} onClick={() => setCat(null)}>All</button>
            {p.menu.map((c) => <button key={c.id} className={cat === c.id ? "chip on" : "chip"} onClick={() => setCat(c.id)}>{c.name}</button>)}
          </div>
          {items.length === 0 ? (
            <div className="state"><p>{q ? "Nothing matches your search." : "The menu is empty right now."}</p></div>
          ) : (
            <ul className="list">
              {items.map((i) => (
                <li key={i.id}>
                  <button className="card" onClick={() => p.onPick(i)}>
                    <span className="card-main">
                      <span className="name">{i.name}</span>
                      <span className="desc">{i.description}</span>
                    </span>
                    <span className="price">{i.variants.length ? "From " : ""}{formatEtb(i.variants.length ? Math.min(...i.variants.map((v) => v.priceSantim)) : i.priceSantim)}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </>
  );
}

function ItemSheet(p: { item: MenuItem; close: () => void; add: (l: CartLine) => void }) {
  const { item } = p;
  const [variantId, setVariantId] = useState<number | undefined>(item.variants[0]?.id);
  const [extraIds, setExtraIds] = useState<number[]>([]);
  const [quantity, setQuantity] = useState(1);
  const [note, setNote] = useState("");
  const line: CartLine = { key: `${item.id}|${variantId ?? ""}|${[...extraIds].sort().join(",")}|${note.trim()}`, item, variantId, extraIds, quantity, note: note.trim() };
  return (
    <div className="backdrop" onClick={p.close}>
      <section className="sheet" role="dialog" aria-modal="true" aria-label={item.name} onClick={(e) => e.stopPropagation()}>
        <h2>{item.name}</h2>
        <p className="desc">{item.description}</p>
        {item.variants.length > 0 && (
          <fieldset><legend>Size</legend>
            {item.variants.map((v) => (
              <label key={v.id} className="opt"><input type="radio" name="variant" checked={variantId === v.id} onChange={() => setVariantId(v.id)} />
                <span>{v.name}</span><span>{formatEtb(v.priceSantim)}</span></label>
            ))}
          </fieldset>
        )}
        {item.extras.length > 0 && (
          <fieldset><legend>Extras</legend>
            {item.extras.map((e) => (
              <label key={e.id} className="opt"><input type="checkbox" checked={extraIds.includes(e.id)}
                onChange={(ev) => setExtraIds(ev.target.checked ? [...extraIds, e.id] : extraIds.filter((x) => x !== e.id))} />
                <span>{e.name}</span><span>+ {formatEtb(e.priceSantim)}</span></label>
            ))}
          </fieldset>
        )}
        <label className="field">Note for the kitchen
          <input value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} placeholder="For example: no onions" />
        </label>
        <div className="row">
          <div className="qty">
            <button aria-label="Fewer" onClick={() => setQuantity(Math.max(1, quantity - 1))}>−</button>
            <span>{quantity}</span>
            <button aria-label="More" onClick={() => setQuantity(Math.min(50, quantity + 1))}>+</button>
          </div>
          <button className="primary grow" onClick={() => p.add(line)}>Add · {formatEtb(linePrice(line))}</button>
        </div>
      </section>
    </div>
  );
}

function CartScreen(p: { cart: CartLine[]; setCart: (c: CartLine[]) => void; estimate: number; back: () => void; next: () => void }) {
  const setQty = (key: string, q: number) => p.setCart(p.cart.map((l) => (l.key === key ? { ...l, quantity: q } : l)).filter((l) => l.quantity > 0));
  return (
    <>
      <TopBar title="Your cart" back={p.back} />
      {p.cart.length === 0 ? (
        <div className="state"><p>Your cart is empty. Pick something from the menu.</p><button className="primary" onClick={p.back}>Browse menu</button></div>
      ) : (
        <>
          <ul className="list">
            {p.cart.map((l) => (
              <li key={l.key} className="cartline">
                <div className="card-main">
                  <span className="name">{l.item.name}{l.variantId ? ` (${l.item.variants.find((v) => v.id === l.variantId)?.name})` : ""}</span>
                  {l.extraIds.length > 0 && <span className="desc">{l.extraIds.map((id) => l.item.extras.find((e) => e.id === id)?.name).join(", ")}</span>}
                  {l.note && <span className="desc">Note: {l.note}</span>}
                  <span className="price">{formatEtb(linePrice(l))}</span>
                </div>
                <div className="qty">
                  <button aria-label={`Remove one ${l.item.name}`} onClick={() => setQty(l.key, l.quantity - 1)}>−</button>
                  <span>{l.quantity}</span>
                  <button aria-label={`Add one ${l.item.name}`} onClick={() => setQty(l.key, Math.min(50, l.quantity + 1))}>+</button>
                </div>
              </li>
            ))}
          </ul>
          <div className="footer">
            <div className="total"><span>Estimated total</span><strong>{formatEtb(p.estimate)}</strong></div>
            <p className="hint">The restaurant confirms the final total when you place the order.</p>
            <button className="primary" onClick={p.next}>Continue</button>
          </div>
        </>
      )}
    </>
  );
}

function CheckoutScreen(p: { cart: CartLine[]; estimate: number; table: TableCtx; retryTable: () => void; back: () => void; done: (o: Order) => void }) {
  const [type, setType] = useState<OrderRequest["orderType"]>(p.table.state === "ok" ? "dine_in" : "takeaway");
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [address, setAddress] = useState("");
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const key = useRef(newKey()); // one key per checkout attempt, so a retry cannot create a second order

  const tableOk = p.table.state === "ok";
  const valid = type === "dine_in" ? tableOk : type === "delivery" ? address.trim() !== "" && phone.trim() !== "" : true;

  async function submit() {
    setBusy(true); setError(null);
    const req: OrderRequest = {
      orderType: type, tableToken: type === "dine_in" ? p.table.token : undefined, customerName: name, customerPhone: phone,
      deliveryAddress: type === "delivery" ? address : "", notes,
      items: p.cart.map((l) => ({ menuItemId: l.item.id, variantId: l.variantId, extraIds: l.extraIds, quantity: l.quantity, note: l.note })),
    };
    try { p.done(await placeOrder(req, key.current)); }
    catch (e) {
      const err = e as ApiError;
      if (err.status === 409 && err.code === "idempotency_conflict") key.current = newKey();
      if (err.code === "table_invalid") { forgetTable(); p.retryTable(); }
      setError(err.message);
    } finally { setBusy(false); }
  }

  return (
    <>
      <TopBar title="Checkout" back={p.back} />
      <div className="form">
        <div className="seg" role="radiogroup" aria-label="Order type">
          {([["dine_in", "Dine in"], ["takeaway", "Takeaway"], ["delivery", "Delivery"]] as const).map(([v, label]) => (
            <button key={v} role="radio" aria-checked={type === v} className={type === v ? "on" : ""} onClick={() => setType(v)}>{label}</button>
          ))}
        </div>
        {type === "dine_in" && (
          tableOk
            ? <div className="tablecard" aria-live="polite">Your table<strong>{p.table.label}</strong><span className="hint">Check this matches the table you are sitting at.</span></div>
            : <div className="error" role="alert">{p.table.state === "loading" ? "Checking your table…" : "Scan the QR code on your table to order for a table, or choose takeaway."}</div>
        )}
        <label className="field">Name (optional)<input value={name} maxLength={80} onChange={(e) => setName(e.target.value)} /></label>
        {type === "delivery" && (
          <>
            <label className="field">Phone number<input type="tel" value={phone} maxLength={20} onChange={(e) => setPhone(e.target.value)} /></label>
            <label className="field">Delivery address<input value={address} maxLength={300} onChange={(e) => setAddress(e.target.value)} /></label>
          </>
        )}
        <label className="field">Order notes (optional)<input value={notes} maxLength={500} onChange={(e) => setNotes(e.target.value)} /></label>
        <div className="notice">Pay at the restaurant. The cashier will confirm your order as paid after receiving cash. Telebirr is not configured yet.</div>
        {error && <div className="error" role="alert">{error}</div>}
        <div className="total"><span>Estimated total</span><strong>{formatEtb(p.estimate)}</strong></div>
        <button className="primary" disabled={!valid || busy} onClick={submit}>{busy ? "Placing order…" : "Place order"}</button>
      </div>
    </>
  );
}

function StatusScreen(p: { token: string; back: () => void }) {
  const [order, setOrder] = useState<Order | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [live, setLive] = useState(false);
  useEffect(() => {
    let stop = false;
    const tick = async () => {
      try { const o = await trackOrder(p.token); if (!stop) { setOrder(o); setError(null); } }
      catch (e) { if (!stop) setError((e as ApiError).message); }
    };
    tick();
    // Real-time updates when the runtime supports streaming; a slow poll is always kept as a safety net.
    const close = connectSse({
      url: `${API_BASE}/api/v1/orders/track/${encodeURIComponent(p.token)}/events`,
      onReady: tick, onEvent: tick, onState: (st) => setLive(st === "live"), onFatal: () => setLive(false),
    });
    const id = setInterval(tick, 20000);
    return () => { stop = true; clearInterval(id); close(); };
  }, [p.token]);

  return (
    <>
      <TopBar title="Your order" back={p.back} />
      {!order && !error && <div className="state" aria-busy="true">Loading order…</div>}
      {error && <div className="error" role="alert">{error} We keep trying automatically.</div>}
      {order && (
        <div className="form">
          <div className="ordernum">Order #{order.orderNumber}</div>
          <div className={`status s-${order.status}`}>{STATUS_TEXT[order.status] ?? order.status}</div>
          <div className="hint">Payment: {PAY_TEXT[order.paymentStatus] ?? order.paymentStatus}{live ? " · Live updates on" : ""}</div>
          <ul className="list">
            {order.items.map((i) => (
              <li key={i.id} className="cartline">
                <div className="card-main"><span className="name">{i.quantity} × {i.name}{i.variantName ? ` (${i.variantName})` : ""}</span>
                  {i.options.length > 0 && <span className="desc">{i.options.map((o) => o.name).join(", ")}</span>}</div>
                <span className="price">{formatEtb(i.lineTotalSantim)}</span>
              </li>
            ))}
          </ul>
          <div className="total"><span>Total</span><strong>{formatEtb(order.totalSantim)}</strong></div>
        </div>
      )}
    </>
  );
}

function TopBar(p: { title: string; back: () => void }) {
  return <header className="topbar"><button aria-label="Back" onClick={p.back}>‹</button><h1>{p.title}</h1></header>;
}
