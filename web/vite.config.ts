import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react()],
  server: {
    // In production Caddy serves the app and proxies the API on one origin
    // (spec §5.1). Proxying the same paths in dev keeps requests same-origin
    // there too, so no code ever needs a base URL and CORS never appears.
    proxy: {
      "/api": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
  build: {
    // Caddy mounts this directory read-only at /srv.
    outDir: "dist",
    sourcemap: true,
  },
});
