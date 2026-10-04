import { defineConfig } from "@playwright/test";

// Production CSP must be tested against compiled static assets served by Caddy.
export default defineConfig({
  testDir: "./tests/security",
  workers: 1,
  use: { baseURL: process.env.PSST_TEST_BASE_URL ?? "http://127.0.0.1:18784" },
});
