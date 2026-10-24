import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// base "./" keeps asset paths relative, which Macle packaging needs (UNVERIFIED: confirm in simulator).
export default defineConfig({
  base: "./",
  plugins: [react()],
  build: { target: "es2018", cssTarget: "chrome61", sourcemap: false },
  server: { port: 5173 },
});
