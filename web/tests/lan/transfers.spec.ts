import { test, expect, type Page } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile, readdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";

const binary = resolve(process.env.PSST_LAN_BINARY ?? "../client/psst");
const payload = Buffer.from("psst.zip LAN encrypted browser transfer\n");

type Startup = { origin: string; link: string; id: string };
async function start(args: string[]) {
  const child = spawn(binary, [...args, "--listen", "127.0.0.1:0", "--json", "--expires", "1h"], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  let diagnostics = "";
  child.stderr.on("data", (chunk) => {
    diagnostics = (diagnostics + chunk).slice(-16384);
  });
  const ended = new Promise<number | null>((resolve, reject) => {
    child.once("exit", resolve);
    child.once("error", reject);
  });
  const lines = createInterface({ input: child.stdout });
  const ready = new Promise<Startup>((resolve, reject) => {
    lines.once("line", (line) => {
      try {
        const value = JSON.parse(line);
        expect(value.schema_version).toBe(1);
        expect(value.ok).toBe(true);
        expect(value.data.link).toMatch(/^http:\/\/127\.0\.0\.1:\d+\/[du]\//);
        resolve(value.data);
      } catch (cause) {
        reject(cause);
      }
    });
    child.once("exit", (code) =>
      reject(new Error(`LAN process exited before readiness (${code}): ${diagnostics}`)),
    );
    child.once("error", reject);
  });
  let timer: ReturnType<typeof setTimeout>;
  try {
    const startup = await Promise.race([
      ready,
      new Promise<never>((_, reject) => {
        timer = setTimeout(
          () => reject(new Error(`LAN startup timed out: ${diagnostics}`)),
          15_000,
        );
      }),
    ]);
    return {
      ...startup,
      ended,
      async stop() {
        if (child.exitCode === null && child.signalCode === null) child.kill("SIGINT");
        const kill = setTimeout(() => child.kill("SIGKILL"), 3000);
        try {
          await ended;
        } finally {
          clearTimeout(kill);
          lines.close();
        }
      },
    };
  } catch (cause) {
    child.kill("SIGKILL");
    await ended.catch(() => {});
    lines.close();
    throw cause;
  } finally {
    clearTimeout(timer!);
  }
}

async function isolateHTTP(page: Page, origin: string) {
  const external: string[] = [];
  await page.route("**/*", async (route) => {
    const url = new URL(route.request().url());
    if (url.origin === origin || ["blob:", "data:"].includes(url.protocol)) await route.continue();
    else {
      external.push(url.origin);
      await route.abort();
    }
  });
  // Loopback HTTP normally receives secure-context APIs. Force the actual LAN
  // crypto and bounded Blob-save paths, without weakening production code.
  await page.addInitScript(() => {
    Object.defineProperty(globalThis.crypto, "subtle", {
      get: () => undefined,
      configurable: true,
    });
    Object.defineProperty(globalThis, "isSecureContext", { value: false, configurable: true });
    Object.defineProperty(navigator, "clipboard", { get: () => undefined, configurable: true });
  });
  return external;
}

test("embedded HTTP Send decrypts offline and stops after its download allowance", async ({
  page,
}) => {
  const dir = await mkdtemp(join(tmpdir(), "psst-lan-browser-send-"));
  const file = join(dir, "message.txt");
  await writeFile(file, payload);
  let session: Awaited<ReturnType<typeof start>> | undefined;
  try {
    session = await start([
      "serve",
      "send",
      file,
      "--max-downloads",
      "1",
      "--state-dir",
      join(dir, "state"),
    ]);
    const external = await isolateHTTP(page, session.origin);
    await page.goto(session.link);
    await expect(page.getByText("message.txt", { exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: /settings|sign in/i })).toHaveCount(0);
    for (const route of ["/settings", "/login", "/api/v1/admin/settings", "/api/v1/auth/me"]) {
      expect((await page.request.get(session.origin + route)).status()).toBe(404);
    }
    expect(await page.evaluate(() => globalThis.crypto.subtle)).toBeUndefined();
    const download = page.waitForEvent("download");
    await page.getByRole("button", { name: "Save file", exact: true }).click();
    const saved = await download;
    expect(saved.suggestedFilename()).toBe("message.txt");
    expect(await readFile((await saved.path())!)).toEqual(payload);
    expect(await session.ended).toBe(0);
    expect(external).toEqual([]);
  } finally {
    await session?.stop();
    await rm(dir, { recursive: true, force: true });
  }
});

test("embedded HTTP Receive encrypts offline and enforces the one-file allowance", async ({
  page,
}) => {
  const dir = await mkdtemp(join(tmpdir(), "psst-lan-browser-receive-"));
  let session: Awaited<ReturnType<typeof start>> | undefined;
  try {
    session = await start([
      "serve",
      "receive",
      "--output-dir",
      dir,
      "--max-files",
      "1",
      "--state-dir",
      join(dir, "state"),
    ]);
    const external = await isolateHTTP(page, session.origin);
    await page.goto(session.link);
    await page
      .getByLabel("Choose files", { exact: true })
      .setInputFiles({ name: "received.txt", mimeType: "text/plain", buffer: payload });
    await page.getByRole("button", { name: "Send files", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Files sent", exact: true })).toBeVisible();
    await expect
      .poll(async () => (await readdir(dir)).filter((name) => /^[a-f0-9-]{36}$/.test(name)))
      .toHaveLength(1);
    const submission = (await readdir(dir)).find((name) => /^[a-f0-9-]{36}$/.test(name))!;
    expect(await readFile(join(dir, submission, "received.txt"))).toEqual(payload);
    expect(await session.ended).toBe(0);
    await expect(
      page.request.get(`${session.origin}/api/v1/slots/${session.id}/availability`),
    ).rejects.toThrow();
    expect(external).toEqual([]);
  } finally {
    try {
      await session?.stop();
      // Session cleanup must preserve received plaintext.
      if (session) {
        const submission = (await readdir(dir)).find((name) => /^[a-f0-9-]{36}$/.test(name))!;
        expect(await readFile(join(dir, submission, "received.txt"))).toEqual(payload);
      }
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  }
});
