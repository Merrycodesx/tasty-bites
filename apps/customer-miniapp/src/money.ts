/** Money is kept as integer santim (1 ETB = 100 santim). Never use floats for arithmetic. */
export function formatEtb(santim: number): string {
  const birr = Math.floor(santim / 100);
  const cents = String(santim % 100).padStart(2, "0");
  return `ETB ${birr.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",")}.${cents}`;
}
