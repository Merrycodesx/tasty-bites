export interface Row { orderNumber: number; status: "received" | "preparing" | "ready"; tableLabel?: string }
export interface Columns { received: Row[]; preparing: Row[]; ready: Row[] }

export const COLUMN_LIMIT = 18; // visible tiles per column; the rest is summarised as "+N more"

export function groupRows(rows: Row[]): Columns {
  const c: Columns = { received: [], preparing: [], ready: [] };
  for (const r of rows) if (r.status in c) c[r.status].push(r);
  return c;
}

/** Ready orders are most urgent: newest first is not needed, keep arrival order but cap the visible tiles. */
export function visible(rows: Row[], limit = COLUMN_LIMIT): { shown: Row[]; more: number } {
  return { shown: rows.slice(0, limit), more: Math.max(0, rows.length - limit) };
}

/** Pairing token comes from the URL fragment (never sent to a server or logged), then is removed from the address bar. */
export function takeToken(hash: string): string | null {
  const m = /(?:^#|&)token=([0-9a-f]{64})(?:&|$)/.exec(hash);
  return m ? m[1] : null;
}
