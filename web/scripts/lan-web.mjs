// Run the LAN build/check with the same environment on Unix and native Windows.
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const commands = {
  build: [["vite/bin/vite.js", "build"]],
  check: [
    ["@sveltejs/kit/svelte-kit.js", "sync"],
    ["svelte-check/bin/svelte-check", "--tsconfig", "./tsconfig.json"],
  ],
};
const selected = commands[process.argv[2]];
if (!selected) throw new Error("Use lan-web.mjs build or check");
for (const [entry, ...args] of selected) {
  const result = spawnSync(
    process.execPath,
    [fileURLToPath(new URL("../node_modules/" + entry, import.meta.url)), ...args],
    { stdio: "inherit", env: { ...process.env, PSST_LAN_WEB: "1" } },
  );
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}
