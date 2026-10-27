import { describe, expect, it } from "vitest";
import { COLUMN_LIMIT, groupRows, takeToken, visible, Row } from "./board";

const row = (n: number, status: Row["status"]): Row => ({ orderNumber: n, status });

describe("board", () => {
  it("groups by status", () => {
    const g = groupRows([row(1, "received"), row(2, "preparing"), row(3, "ready"), row(4, "ready")]);
    expect(g.received).toHaveLength(1); expect(g.ready.map((r) => r.orderNumber)).toEqual([3, 4]);
  });
  it("caps visible tiles and counts the rest", () => {
    const many = Array.from({ length: COLUMN_LIMIT + 7 }, (_, i) => row(i, "ready"));
    const v = visible(many);
    expect(v.shown).toHaveLength(COLUMN_LIMIT); expect(v.more).toBe(7);
  });
  it("reads a pairing token only if it is 64 hex chars", () => {
    const t = "a".repeat(64);
    expect(takeToken(`#token=${t}`)).toBe(t);
    expect(takeToken("#token=short")).toBeNull();
    expect(takeToken("")).toBeNull();
  });
});
