import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { federation } from "@module-federation/vite";
import federationConfig from "./module-federation.config";

export default defineConfig({
  plugins: [react(), federation(federationConfig)],
  server: { host: "127.0.0.1", port: 4301, strictPort: true, cors: true },
  build: { target: "es2022", sourcemap: true },
});
