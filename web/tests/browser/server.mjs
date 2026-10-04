import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
const directory = mkdtempSync(join(tmpdir(), "psst-browser-"));
try {
  execFileSync("go", ["build", "-o", join(directory, "server"), "./cmd/server"], {
    cwd: resolve("../backend"),
  });
} catch (error) {
  rmSync(directory, { recursive: true, force: true });
  throw error;
}
const server = spawn(join(directory, "server"), [], {
  env: {
    ...process.env,
    ADMIN_USERNAME: "admin",
    ADMIN_PASSWORD: "Test-admin-password-2026",
    AUTH_ALLOW_INSECURE_HTTP: "true",
    PUBLIC_URL: "http://127.0.0.1:4173",
    LISTEN_ADDR: `127.0.0.1:${process.env.PSST_TEST_BACKEND_PORT || "8080"}`,
    DB_PATH: join(directory, "psst.db"),
    STORAGE_PATH: join(directory, "files"),
  },
  stdio: "inherit",
});
for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, () => server.kill(signal));
server.on("exit", (code) => {
  rmSync(directory, { recursive: true, force: true });
  process.exit(code ?? 0);
});
