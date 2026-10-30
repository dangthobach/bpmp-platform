import { resolve } from "node:path";
import { defineConfig } from "vitest/config";

const rootNodeModules = resolve(__dirname, "../../../node_modules");

export default defineConfig({
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
  },
  resolve: {
    dedupe: ["react", "react-dom"],
    alias: {
      react: resolve(rootNodeModules, "react"),
      "react-dom": resolve(rootNodeModules, "react-dom"),
    },
  },
});
