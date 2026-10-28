import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  fullyParallel: false,
  use: {
    baseURL: "http://127.0.0.1:4300",
    trace: "retain-on-failure",
  },
  webServer: [
    {
      command: "npm run dev -w @ods/task-mfe",
      url: "http://127.0.0.1:4301/mf-manifest.json",
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
    },
    {
      command: "npm run dev -w @ods/shell",
      url: "http://127.0.0.1:4300",
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
    },
  ],
});
