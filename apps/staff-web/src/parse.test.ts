import { describe, expect, it } from "vitest";
import { parseEtb } from "./parse";

describe("parseEtb", () => {
  it("parses exact amounts", () => {
    expect(parseEtb("120")).toBe(12000);
    expect(parseEtb("120.5")).toBe(12050);
    expect(parseEtb("0.07")).toBe(7);
  });
  it("rejects bad input", () => {
    for (const bad of ["", "abc", "-5", "1.234", "1,000", "12e3"]) expect(parseEtb(bad)).toBeNull();
  });
});
