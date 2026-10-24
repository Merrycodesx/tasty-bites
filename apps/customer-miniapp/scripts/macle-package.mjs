// Copies the preserved Macle config files into dist/ so dist/ is a self-contained Macle project.
// UNVERIFIED: whether the toolkit expects this layout. See docs/macle-notes.md and test in your simulator.
import { copyFileSync, existsSync } from "node:fs";

for (const f of [".macle.config.json", "app-config.json", "project.config.json"]) {
  if (!existsSync(f)) { console.error(`Missing ${f}`); process.exit(1); }
  copyFileSync(f, `dist/${f}`);
  console.log(`copied ${f} -> dist/${f}`);
}
