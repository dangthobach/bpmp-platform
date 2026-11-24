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
      "/auth": { target: process.env.ODS_API_TARGET ?? process.env.ODS_CASE_API_TARGET ?? "http://127.0.0.1:8085", changeOrigin: false, secure: false },
      "/v1/customer-cases": { target: process.env.ODS_CASE_API_TARGET ?? process.env.ODS_API_TARGET ?? "http://127.0.0.1:8085", changeOrigin: false, secure: false },
      "/v1": { target: process.env.ODS_API_TARGET ?? "https://localhost:8443", changeOrigin: true, secure: false },
      "/realtime": { target: process.env.ODS_REALTIME_TARGET ?? process.env.ODS_API_TARGET ?? "https://localhost:8443", changeOrigin: true, secure: false },
    },
  },
  build: { target: "es2022", sourcemap: true },
});
