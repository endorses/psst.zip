import adapter from "@sveltejs/adapter-static";
import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";

/** @type {import('@sveltejs/kit').Config} */
const standalone = process.env.PSST_LAN_WEB === "1";
const config = {
  preprocess: vitePreprocess(),
  kit: {
    ...(standalone ? { files: { routes: "src/lan/routes" } } : {}),
    adapter: adapter({
      fallback: "index.html",
      ...(standalone ? { pages: "build-lan", assets: "build-lan" } : {}),
    }),
    csp: {
      mode: "hash",
      directives: {
        "default-src": ["self"],
        "script-src": ["self"],
        "script-src-attr": ["none"],
        // Svelte transitions and progress indicators set inline styles.
        "style-src": ["self", "unsafe-inline"],
        "img-src": ["self", "data:", "blob:"],
        "media-src": ["self", "blob:"],
        "worker-src": ["self", "blob:"],
        "connect-src": ["self"],
        "object-src": ["none"],
        "base-uri": ["none"],
        "form-action": ["self"],
        "frame-ancestors": ["none"],
      },
    },
  },
};

export default config;
