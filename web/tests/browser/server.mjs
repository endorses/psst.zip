import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, sep } from "node:path";
const directory = mkdtempSync(join(tmpdir(), "psst-browser-"));
// Opt-in local integration tests may age only this disposable database.
const stateFile = process.env.PSST_TEST_STATE_FILE;
let stateFileWritten = false;
try {
  execFileSync("go", ["build", "-o", join(directory, "server"), "./cmd/server"], {
    cwd: resolve("../backend"),
  });
  if (stateFile) {
    if (!resolve(stateFile).startsWith(resolve(tmpdir()) + sep))
      throw new Error("Browser test state must be under the temporary directory");
    writeFileSync(
      stateFile,
      JSON.stringify({ directory, db: join(directory, "psst.db"), runnerPID: process.pid }),
      {
        flag: "wx",
        mode: 0o600,
      },
    );
    stateFileWritten = true;
  }
} catch (error) {
  rmSync(directory, { recursive: true, force: true });
  throw error;
}
let restarting = false;
let shuttingDown = false;
function startServer() {
  const child = spawn(join(directory, "server"), [], {
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
  child.on("exit", (code) => {
    if (restarting && !shuttingDown) {
      restarting = false;
      server = startServer();
      return;
    }
    if (stateFileWritten) rmSync(stateFile, { force: true });
    rmSync(directory, { recursive: true, force: true });
    process.exit(code ?? 0);
  });
  return child;
}
let server = startServer();
for (const signal of ["SIGTERM", "SIGINT"])
  process.on(signal, () => {
    shuttingDown = true;
    server.kill(signal);
  });
// Only opt-in disposable fixtures expose restart control; no production endpoint.
if (stateFile)
  process.on("SIGUSR2", () => {
    if (!shuttingDown && !restarting) {
      restarting = true;
      server.kill("SIGTERM");
    }
  });
