import { useCallback, useEffect, useRef, useState } from "react";
import { Row, groupRows, takeToken, visible } from "./board";
import { connectSse, SseState } from "./sse";

const BASE = (import.meta.env.VITE_API_URL as string | undefined) ?? "http://localhost:8080";
const TOKEN_KEY = "tb_tv_token";

type Phase = "unpaired" | "loading" | "ready" | "revoked" | "error";

function loadToken(): string | null {
  try {
    const fromHash = takeToken(window.location.hash);
    if (fromHash) {
      localStorage.setItem(TOKEN_KEY, fromHash);
      window.history.replaceState(null, "", window.location.pathname + window.location.search); // remove the token from the address bar
      return fromHash;
    }
    return localStorage.getItem(TOKEN_KEY);
  } catch { return null; }
}

export default function App() {
  const [token, setToken] = useState<string | null>(loadToken);
  const [phase, setPhase] = useState<Phase>(token ? "loading" : "unpaired");
  const [rows, setRows] = useState<Row[]>([]);
  const [conn, setConn] = useState<SseState>("connecting");
  const [lastOk, setLastOk] = useState(0);
  const [now, setNow] = useState(Date.now());
  const debounce = useRef<ReturnType<typeof setTimeout>>();

  const refresh = useCallback(async () => {
    if (!token) return;
    try {
      const res = await fetch(`${BASE}/api/v1/display/orders`, { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" });
      if (res.status === 401) { try { localStorage.removeItem(TOKEN_KEY); } catch { /* ignore */ } setToken(null); setPhase("revoked"); return; }
      if (!res.ok) throw new Error(String(res.status));
      const data = (await res.json()) as { orders: Row[] };
      setRows(data.orders); setPhase("ready"); setLastOk(Date.now());
    } catch { setPhase((p) => (p === "ready" ? p : "error")); }
  }, [token]);

  useEffect(() => {
    if (!token) return;
    refresh();
    const close = connectSse({
      url: `${BASE}/api/v1/display/events`, headers: { Authorization: `Bearer ${token}` },
      onReady: refresh,
      onEvent: () => { clearTimeout(debounce.current); debounce.current = setTimeout(refresh, 250); },
      onState: setConn,
      onFatal: () => refresh(), // 401 -> refresh() shows the revoked screen
    });
    const poll = setInterval(refresh, 30000); // safety net, and expires old "ready" orders
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => { close(); clearInterval(poll); clearInterval(tick); clearTimeout(debounce.current); };
  }, [token, refresh]);

  if (phase === "unpaired") return <Message title="Display not paired" text="Ask the restaurant manager for this display's pairing link and open it on this TV." />;
  if (phase === "revoked") return <Message title="Display access ended" text="This display was revoked or its pairing is no longer valid. Ask the manager to pair it again." />;
  if (phase === "loading") return <Message title="Loading orders…" text="" />;
  if (phase === "error" && rows.length === 0) return <Message title="Cannot reach the restaurant system" text="Retrying automatically." />;

  const stale = now - lastOk > 90000;
  const cols = groupRows(rows);
  const time = new Date(now).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return (
    <div className="tv">
      <header><h1>Tasty Bites</h1><div className="clock">{time}</div></header>
      {(conn === "offline" || stale) && <div className="banner" role="alert">{stale ? "Connection lost — this board may be out of date. Reconnecting…" : "Reconnecting…"}</div>}
      {rows.length === 0 ? (
        <div className="empty">No active orders</div>
      ) : (
        <main>
          <Column title="Order received" tone="received" rows={cols.received} />
          <Column title="Preparing" tone="preparing" rows={cols.preparing} />
          <Column title="Ready for pickup" tone="ready" rows={cols.ready} />
        </main>
      )}
    </div>
  );
}

function Column(p: { title: string; tone: string; rows: Row[] }) {
  const v = visible(p.rows);
  return (
    <section className={`col ${p.tone}`} aria-label={p.title}>
      <h2>{p.title}</h2>
      <div className="tiles">
        {v.shown.map((r) => (
          <div key={r.orderNumber} className="tile"><span className="num">{r.orderNumber}</span>{r.tableLabel && <span className="tbl">Table {r.tableLabel}</span>}</div>
        ))}
      </div>
      {v.more > 0 && <div className="more">+{v.more} more</div>}
    </section>
  );
}

function Message(p: { title: string; text: string }) {
  return <div className="msg"><h1>{p.title}</h1><p>{p.text}</p></div>;
}
