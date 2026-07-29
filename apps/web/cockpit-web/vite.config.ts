import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    port: 4173,
    strictPort: false,
    proxy: {
      "/v1": {
        target: "https://localhost:8443",
        changeOrigin: true,
        secure: false,
      },
      "/api/v1": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
      "/realtime": {
        target: "https://localhost:7601",
        changeOrigin: true,
        secure: false,
      },
    },
  },
  build: {
    target: "es2022",
    sourcemap: true,
    chunkSizeWarningLimit: 650,
  },
});
