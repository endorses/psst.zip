import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/lan",
  workers: 1,
  timeout: 30_000,
  use: { locale: "en-US", actionTimeout: 10_000, acceptDownloads: true },
});
