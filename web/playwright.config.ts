import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/browser",
  workers: 1,
  use: { baseURL: "http://127.0.0.1:4173" },
  webServer: [
    {
      command: "node tests/browser/server.mjs",
      url: `http://127.0.0.1:${process.env.PSST_TEST_BACKEND_PORT || "8080"}/api/v1/slots/invalid`,
      timeout: 60000,
      gracefulShutdown: { signal: "SIGTERM", timeout: 5000 },
    },
    {
      command: "npm run dev -- --host 127.0.0.1 --port 4173 --strictPort",
      url: "http://127.0.0.1:4173",
      timeout: 30000,
    },
  ],
});
