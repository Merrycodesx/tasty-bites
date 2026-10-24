import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, Category, MenuItem, Order, OrderRequest, TableRef, getMenu, getTables, placeOrder, trackOrder } from "./api";
import { formatEtb } from "./money";

interface CartLine { key: string; item: MenuItem; variantId?: number; extraIds: number[]; quantity: number; note: string }
type Screen = "menu" | "cart" | "checkout" | "status";

const TOKEN_KEY = "tb_last_order_token";
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
        <MenuScreen menu={menu} error={menuError} retry={loadMenu} onPick={setPicked} hasOrder={!!token}
          onTrack={() => setScreen("status")} />
      )}
      {screen === "cart" && (
        <CartScreen cart={cart} setCart={setCart} estimate={estimate} back={() => setScreen("menu")} next={() => setScreen("checkout")} />
      )}
      {screen === "checkout" && (
        <CheckoutScreen cart={cart} estimate={estimate} back={() => setScreen("cart")}
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

function MenuScreen(p: { menu: Category[] | null; error: string | null; retry: () => void; onPick: (i: MenuItem) => void; hasOrder: boolean; onTrack: () => void }) {
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
        <input className="search" type="search" placeholder="Search the menu" aria-label="Search the menu" value={q} onChange={(e) => setQ(e.target.value)} />
      </header>
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

function CheckoutScreen(p: { cart: CartLine[]; estimate: number; back: () => void; done: (o: Order) => void }) {
  const [type, setType] = useState<OrderRequest["orderType"]>("dine_in");
  const [tables, setTables] = useState<TableRef[]>([]);
  const [tableId, setTableId] = useState<number | undefined>();
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [address, setAddress] = useState("");
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const key = useRef(newKey()); // one key per checkout attempt, so a retry cannot create a second order
  useEffect(() => { getTables().then((t) => setTables(t.tables)).catch(() => setTables([])); }, []);

  const valid = type === "dine_in" ? !!tableId : type === "delivery" ? address.trim() !== "" && phone.trim() !== "" : true;

  async function submit() {
    setBusy(true); setError(null);
    const req: OrderRequest = {
      orderType: type, tableId: type === "dine_in" ? tableId : undefined, customerName: name, customerPhone: phone,
      deliveryAddress: type === "delivery" ? address : "", notes,
      items: p.cart.map((l) => ({ menuItemId: l.item.id, variantId: l.variantId, extraIds: l.extraIds, quantity: l.quantity, note: l.note })),
    };
    try { p.done(await placeOrder(req, key.current)); }
    catch (e) {
      const err = e as ApiError;
      if (err.status === 409 && err.code === "idempotency_conflict") key.current = newKey();
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
          <label className="field">Your table
            <select value={tableId ?? ""} onChange={(e) => setTableId(e.target.value ? Number(e.target.value) : undefined)}>
              <option value="">Choose a table</option>
              {tables.map((t) => <option key={t.id} value={t.id}>{t.label}</option>)}
            </select>
          </label>
        )}
        <label className="field">Name (optional)<input value={name} maxLength={80} onChange={(e) => setName(e.target.value)} /></label>
        {type === "delivery" && (
          <>
            <label className="field">Phone number<input type="tel" value={phone} maxLength={20} onChange={(e) => setPhone(e.target.value)} /></label>
            <label className="field">Delivery address<input value={address} maxLength={300} onChange={(e) => setAddress(e.target.value)} /></label>
          </>
        )}
        <label className="field">Order notes (optional)<input value={notes} maxLength={500} onChange={(e) => setNotes(e.target.value)} /></label>
        <div className="notice">Online payment is not available yet. You pay the restaurant directly.</div>
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
  useEffect(() => {
    let stop = false;
    const tick = async () => {
      try { const o = await trackOrder(p.token); if (!stop) { setOrder(o); setError(null); } }
      catch (e) { if (!stop) setError((e as ApiError).message); }
    };
    tick();
    const id = setInterval(tick, 5000);
    return () => { stop = true; clearInterval(id); };
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
          <div className="hint">Payment: {PAY_TEXT[order.paymentStatus] ?? order.paymentStatus}</div>
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
