import { describe, expect, it } from "vitest";
import { formatEtb } from "./money";

describe("formatEtb", () => {
  it("formats santim exactly", () => {
    expect(formatEtb(0)).toBe("ETB 0.00");
    expect(formatEtb(4050)).toBe("ETB 40.50");
    expect(formatEtb(123456789)).toBe("ETB 1,234,567.89");
  });
});
