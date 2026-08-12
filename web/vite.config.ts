import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Production build is embedded into temperci-control via go:embed.
// Output: internal/webui/dist/{index.html,assets/*}
export default defineConfig({
  plugins: [react()],
  base: "/",
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    assetsDir: "assets",
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/healthz": "http://127.0.0.1:8080",
      "/webhooks": "http://127.0.0.1:8080",
      "/v1": "http://127.0.0.1:8080",
    },
  },
});
