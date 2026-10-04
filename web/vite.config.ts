import { sveltekit } from "@sveltejs/kit/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [sveltekit()],
  server: {
    proxy: {
      "/api": process.env.PSST_TEST_BACKEND_URL || "http://localhost:8080",
    },
  },
});
