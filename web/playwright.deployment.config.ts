import { defineConfig } from "@playwright/test";

// Run against an isolated deployment started by the caller, not a production instance.
export default defineConfig({
  testDir: "./tests/browser",
  workers: 1,
  use: {
    baseURL: process.env.PSST_TEST_BASE_URL ?? "http://127.0.0.1:18480",
  },
});
