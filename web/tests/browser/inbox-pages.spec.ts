import { test, expect, type Page } from "@playwright/test";
import {
  inboxID,
  inboxOwner,
  inboxKey,
  inboxChild,
  inboxCursor,
  inboxPage,
} from "../inbox-page-fixture";
async function setup(page: Page) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: { user: { id: inboxOwner, username: "owner", role: "user", disabled: false } },
    }),
  );
  await page.addInitScript(
    ({ owner, id, key }) =>
      localStorage.setItem(
        `psst.receive-key.v2.${owner}.${id}`,
        JSON.stringify({ publicKey: key, privateKey: key }),
      ),
    { owner: inboxOwner, id: inboxID, key: inboxKey },
  );
}
const open = (page: Page) => page.goto(`/?view=receive&slot=${inboxID}`);
test("empty filtered pages stay navigable, totals remain global, and only the visible page is polled", async ({
  page,
}) => {
  await setup(page);
  await page.setViewportSize({ width: 390, height: 844 });
  const reads: string[] = [];
  let arrivals = false;
  await page.route(`**/api/v1/slots/${inboxID}/inbox?*`, (route) => {
    const q = new URL(route.request().url()).searchParams;
    expect(q.get("limit")).toBe("50");
    const after = q.get("after") || "";
    reads.push(after);
    return route.fulfill({
      json: inboxPage(
        after
          ? {
              transfers: arrivals
                ? [{ transfer_id: inboxChild(3), status: "complete", file_count: 3 }]
                : [],
              next_cursor: inboxCursor(2),
              summary: {
                state: "updating",
                completed_files: null,
                file_count: null,
                total_size: null,
              },
            }
          : { next_cursor: inboxCursor(1) },
      ),
    });
  });
  await open(page);
  const panel = page.getByRole("region", { name: "Received files", exact: true });
  await expect(panel.getByRole("link", { name: "1 file · Save files", exact: true })).toBeVisible();
  await expect(page.getByText(/151 completed files/)).toBeVisible();
  expect(reads).toEqual([""]);
  await panel.getByRole("button", { name: "Next", exact: true }).click();
  await expect(panel.getByText("Page 2", { exact: true })).toBeVisible();
  await expect(panel.getByText(/No completed submissions on this page/)).toBeVisible();
  await expect(panel.getByRole("button", { name: "Next", exact: true })).toBeEnabled();
  await expect(page.getByText(/Received file totals are updating/)).toBeVisible();
  arrivals = true;
  await expect(panel.getByRole("link", { name: "3 files · Save files", exact: true })).toBeVisible({
    timeout: 10000,
  });
  expect(reads.slice(1).every((c) => c === inboxCursor(1))).toBe(true);
  await panel.getByRole("button", { name: "Previous", exact: true }).click();
  await expect(panel.getByText("Page 1", { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
test("a failed or malformed next page preserves prior entries and cursor", async ({ page }) => {
  await setup(page);
  let malformed = false;
  await page.route(`**/api/v1/slots/${inboxID}/inbox?*`, (route) => {
    const after = new URL(route.request().url()).searchParams.get("after");
    return route.fulfill(
      after
        ? malformed
          ? { json: { ...inboxPage(), paginated: false } }
          : { status: 503, json: { error: "private path" } }
        : { json: inboxPage({ next_cursor: inboxCursor(1) }) },
    );
  });
  await open(page);
  const panel = page.getByRole("region", { name: "Received files", exact: true });
  for (malformed of [false, true]) {
    await panel.getByRole("button", { name: "Next", exact: true }).click();
    await expect(panel.getByRole("alert")).toContainText("Could not refresh");
    await expect(
      panel.getByRole("link", { name: "1 file · Save files", exact: true }),
    ).toBeVisible();
    await expect(panel.getByText("Page 1", { exact: true })).toBeVisible();
  }
});
test("an old inbox 404 cannot clear a newly opened inbox", async ({ page }) => {
  await setup(page);
  let release!: () => void;
  const delayed = new Promise<void>((resolve) => (release = resolve));
  let entered!: () => void;
  const started = new Promise<void>((resolve) => (entered = resolve));
  const other = inboxChild(99);
  await page.route(`**/api/v1/slots/${inboxID}/inbox?*`, async (route) => {
    entered();
    await delayed;
    await route.fulfill({ status: 404, json: { error: "gone" } }).catch(() => {});
  });
  await page.route(`**/api/v1/slots/${other}/inbox?*`, (route) =>
    route.fulfill({ json: inboxPage({ id: other }) }),
  );
  await open(page);
  await started;
  await page.evaluate((id) => {
    history.pushState({}, "", `/?view=receive&slot=${id}`);
    dispatchEvent(new PopStateEvent("popstate"));
  }, other);
  release();
  await expect(page.getByText(/151 completed files/)).toBeVisible();
  await expect(page.getByText(/This receive link has expired/)).toHaveCount(0);
});

import { test as authenticatedTest, signIn } from "./auth-fixture";
import { readFileSync, realpathSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import { execFileSync } from "node:child_process";
function seedEarlierSubmissions(slotID: string, lastID: string) {
  const state = JSON.parse(readFileSync(process.env.PSST_TEST_STATE_FILE!, "utf8"));
  const directory = realpathSync(state.directory),
    db = realpathSync(state.db);
  if (
    dirname(directory) !== realpathSync(tmpdir()) ||
    !basename(directory).startsWith("psst-browser-") ||
    db !== join(directory, "psst.db")
  )
    throw new Error("Refusing to modify anything except the disposable harness database");
  execFileSync("python3", [
    "-c",
    `import sqlite3,sys
with sqlite3.connect(sys.argv[1]) as db:
 db.execute('PRAGMA foreign_keys=ON')
 owner,expiry=db.execute('SELECT owner_id,expires_at FROM slots WHERE id=?',(sys.argv[2],)).fetchone()
 for n in range(100):
  id='00000000-0000-4000-8000-%012d'%n
  assert id < sys.argv[3]
  db.execute("INSERT INTO transfers(id,status,expires_at,owner_id) VALUES (?,'complete',?,?)",(id,expiry,owner))
  db.execute('INSERT INTO slot_transfers(slot_id,transfer_id) VALUES (?,?)',(sys.argv[2],id))
`,
    db,
    slotID,
    lastID,
  ]);
}
authenticatedTest(
  "real 101-submission inbox pages to and saves a later submission",
  async ({ page, request }) => {
    authenticatedTest.skip(
      !process.env.PSST_TEST_STATE_FILE,
      "Requires the explicitly isolated disposable database fixture",
    );
    await signIn(page);
    await page.getByRole("link", { name: "Receive", exact: true }).click();
    await page.getByRole("button", { name: "Create receive link", exact: true }).click();
    const link = await page.getByLabel("Full link").inputValue();
    const slotID = new URL(link).pathname.split("/").pop()!;
    await page.goto(link);
    const contents = Buffer.from("later inbox page saved safely");
    await page
      .getByLabel("Choose files")
      .setInputFiles({ name: "later-page.txt", mimeType: "text/plain", buffer: contents });
    const created = page.waitForResponse(
      (r) => r.url().endsWith(`/slots/${slotID}/transfers`) && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: "Send files", exact: true }).click();
    const child = await (await created).json();
    await expect(page.getByRole("heading", { name: "Files sent" })).toBeVisible();
    seedEarlierSubmissions(slotID, child.id);
    expect((await request.get(`/api/v1/slots/${slotID}`)).status()).toBe(409);
    await page.goto(`/?view=receive&slot=${slotID}`);
    const panel = page.getByRole("region", { name: "Received files", exact: true });
    await expect(panel.getByRole("link")).toHaveCount(50);
    await expect(page.getByText(/1 completed files/)).toBeVisible();
    await panel.getByRole("button", { name: "Next", exact: true }).click();
    await expect(panel.getByText("Page 2", { exact: true })).toBeVisible();
    await expect(panel.getByRole("link")).toHaveCount(50);
    await panel.getByRole("button", { name: "Next", exact: true }).click();
    await expect(panel.getByText("Page 3", { exact: true })).toBeVisible();
    await expect(panel.getByRole("link")).toHaveCount(1);
    await expect(panel.getByRole("button", { name: "Next", exact: true })).toBeDisabled();
    await panel.getByRole("link", { name: "1 file · Save files", exact: true }).click();
    await expect(page.getByText("later-page.txt", { exact: true })).toBeVisible();
    const saved = page.waitForEvent("download");
    await page.getByRole("button", { name: "Save file", exact: true }).click();
    expect(await readFile((await (await saved).path())!)).toEqual(contents);
  },
);
