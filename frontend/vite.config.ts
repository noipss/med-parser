import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// В dev-режиме API проксируется на Go-сервер.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { "/api": "http://127.0.0.1:8765" },
  },
  build: { outDir: "dist", emptyOutDir: true },
});
