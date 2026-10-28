import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { federation } from "@module-federation/vite";
import federationConfig from "./module-federation.config";

export default defineConfig({
  plugins: [react(), federation(federationConfig)],
  server: {
    host: "127.0.0.1",
    port: 4300,
    strictPort: true,
    proxy: {
      "/v1": { target: "https://localhost:8443", changeOrigin: true, secure: false },
      "/realtime": { target: "https://localhost:7601", changeOrigin: true, secure: false },
    },
  },
  build: { target: "es2022", sourcemap: true },
});
