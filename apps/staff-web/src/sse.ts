/**
 * Minimal Server-Sent Events client built on fetch streaming (so it can send an Authorization header).
 * Handles reconnect with backoff, Last-Event-ID resume, stalled-connection detection and auth failures.
 * The API stays the source of truth: callers re-read state on every `onReady` (initial and after reconnects).
 */
export interface SseFrame { id?: string; name: string; data: string }

/** Splits a text buffer into complete SSE frames; returns the frames and the unconsumed remainder. */
export function parseFrames(buffer: string): { frames: SseFrame[]; rest: string } {
  const frames: SseFrame[] = [];
  const parts = buffer.replace(/\r\n/g, "\n").split("\n\n");
  const rest = parts.pop() ?? "";
  for (const part of parts) {
    let id: string | undefined, name = "message", data = "";
    for (const line of part.split("\n")) {
      if (line.startsWith(":")) continue; // comment / heartbeat
      if (line.startsWith("id:")) id = line.slice(3).trim();
      else if (line.startsWith("event:")) name = line.slice(6).trim();
      else if (line.startsWith("data:")) data += (data ? "\n" : "") + line.slice(5).trimStart();
    }
    if (data || name !== "message") frames.push({ id, name, data });
  }
  return { frames, rest };
}

export type SseState = "connecting" | "live" | "offline" | "unsupported";
export interface SseOptions {
  url: string;
  headers?: Record<string, string>;
  onReady: () => void;                 // connected (or reconnected): re-read current state from the API
  onEvent: (data: unknown) => void;    // a committed change happened
  onState?: (s: SseState) => void;
  onFatal?: (status: number) => void;  // 401/403/404: do not retry
  stallMs?: number;                    // no bytes for this long => reconnect (server pings every 15 s)
  fetchImpl?: typeof fetch;
}

export function connectSse(o: SseOptions): () => void {
  const doFetch = o.fetchImpl ?? fetch;
  let stopped = false, lastId = "", attempt = 0;
  let ctrl: AbortController | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;

  async function run() {
    while (!stopped) {
      ctrl = new AbortController();
      let stall: ReturnType<typeof setTimeout> | undefined;
      const arm = () => { clearTimeout(stall); stall = setTimeout(() => ctrl?.abort(), o.stallMs ?? 45000); };
      try {
        o.onState?.("connecting");
        const res = await doFetch(o.url, {
          signal: ctrl.signal,
          headers: { Accept: "text/event-stream", ...(o.headers ?? {}), ...(lastId ? { "Last-Event-ID": lastId } : {}) },
        });
        if (res.status === 401 || res.status === 403 || res.status === 404) { o.onFatal?.(res.status); return; }
        if (!res.ok || !res.body || !res.body.getReader) {
          if (res.ok) { o.onState?.("unsupported"); return; }
          throw new Error(`status ${res.status}`);
        }
        const reader = res.body.getReader();
        const dec = new TextDecoder();
        let buf = "";
        arm();
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          arm();
          buf += dec.decode(value, { stream: true });
          const { frames, rest } = parseFrames(buf);
          buf = rest;
          for (const f of frames) {
            if (f.id) lastId = f.id;
            if (f.name === "ready") { attempt = 0; o.onState?.("live"); o.onReady(); }
            else if (f.name === "resync") { lastId = ""; throw new Error("resync"); } // reconnect fresh; onReady will reload state
            else if (f.name === "unauthorized") { o.onFatal?.(401); return; }
            else if (f.name === "order") { try { o.onEvent(JSON.parse(f.data)); } catch { /* ignore malformed */ } }
          }
        }
      } catch { /* network error, abort, resync: fall through to retry */ }
      clearTimeout(stall);
      if (stopped) return;
      o.onState?.("offline");
      const delay = Math.min(30000, 1000 * 2 ** Math.min(attempt++, 5)) * (0.75 + Math.random() * 0.5);
      await new Promise<void>((r) => { timer = setTimeout(r, delay); });
    }
  }
  run();
  return () => { stopped = true; clearTimeout(timer); ctrl?.abort(); };
}
