import { defineConfig } from "@playwright/test";
// Scanner tests mock account APIs and never need a live transfer server.
export default defineConfig({
  testDir: "./tests/browser",
  testMatch: "scanner.spec.ts",
  workers: 1,
  use: { baseURL: "http://127.0.0.1:4175" },
  webServer: {
    command: "npm run dev -- --host 127.0.0.1 --port 4175 --strictPort",
    url: "http://127.0.0.1:4175",
    timeout: 30000,
  },
});
