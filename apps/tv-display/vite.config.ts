import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Older Android TV WebViews: keep the build target conservative.
export default defineConfig({ plugins: [react()], build: { target: "es2018", cssTarget: "chrome61" }, server: { port: 5175 } });
