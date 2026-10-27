import { describe, expect, it } from "vitest";
import { connectSse, parseFrames } from "./sse";

describe("parseFrames", () => {
  it("parses complete frames and keeps the partial remainder", () => {
    const { frames, rest } = parseFrames('id: 7\nevent: order\ndata: {"a":1}\n\n: ping\n\nevent: ready\ndata: {}\n\nevent: or');
    expect(frames).toEqual([
      { id: "7", name: "order", data: '{"a":1}' },
      { id: undefined, name: "ready", data: "{}" },
    ]);
    expect(rest).toBe("event: or");
  });
  it("handles CRLF", () => {
    expect(parseFrames("event: ready\r\ndata: {}\r\n\r\n").frames).toHaveLength(1);
  });
});

function streamOf(text: string, thenClose = true): Response {
  const enc = new TextEncoder();
  const body = new ReadableStream({
    start(c) { c.enqueue(enc.encode(text)); if (thenClose) c.close(); },
  });
  return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } });
}

describe("connectSse", () => {
  it("calls onReady/onEvent, reconnects with Last-Event-ID and re-reads state", async () => {
    const seen: string[] = []; const headers: (string | null)[] = []; let ready = 0; let n = 0;
    const fetchImpl = (async (_u: string, init: RequestInit) => {
      headers.push(new Headers(init.headers).get("Last-Event-ID"));
      n++;
      if (n === 1) return streamOf('event: ready\ndata: {}\n\nid: 5\nevent: order\ndata: {"status":"preparing"}\n\n');
      return streamOf('event: ready\ndata: {}\n\n', false); // second connection stays open
    }) as unknown as typeof fetch;
    const stop = connectSse({ url: "x", fetchImpl, onReady: () => { ready++; }, onEvent: (d) => seen.push((d as { status: string }).status), stallMs: 5000 });
    await new Promise((r) => setTimeout(r, 2500)); // first backoff is ~1 s
    stop();
    expect(seen).toEqual(["preparing"]);
    expect(ready).toBe(2);               // reloaded state after the reconnect
    expect(headers[0]).toBeNull();
    expect(headers[1]).toBe("5");        // resumed from the last event id
  });
  it("stops retrying on 401", async () => {
    let calls = 0; let fatal = 0;
    const fetchImpl = (async () => { calls++; return new Response("", { status: 401 }); }) as unknown as typeof fetch;
    connectSse({ url: "x", fetchImpl, onReady() {}, onEvent() {}, onFatal: (s) => { fatal = s; } });
    await new Promise((r) => setTimeout(r, 100));
    expect(fatal).toBe(401); expect(calls).toBe(1);
  });
});
