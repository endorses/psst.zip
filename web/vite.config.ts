import { sveltekit } from "@sveltejs/kit/vite";
import { defineConfig } from "vite";
import browserModuleInventory from "./scripts/browser-module-inventory.mjs";

export default defineConfig({
  plugins: [
    browserModuleInventory({
      version: process.env.VERSION || "dev",
      revision: process.env.REVISION || "main",
    }),
    sveltekit(),
  ],
  server: {
    proxy: {
      "/api": process.env.PSST_TEST_BACKEND_URL || "http://localhost:8080",
    },
  },
});
