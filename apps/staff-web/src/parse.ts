/** Parses an ETB amount like "120" or "120.5" into integer santim, or null if invalid. No floating point. */
export function parseEtb(input: string): number | null {
  const m = /^\s*(\d{1,7})(?:\.(\d{1,2}))?\s*$/.exec(input);
  if (!m) return null;
  return Number(m[1]) * 100 + Number((m[2] ?? "").padEnd(2, "0") || 0);
}
