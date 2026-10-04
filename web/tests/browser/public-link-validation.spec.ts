import { test, expect } from "@playwright/test";
import { randomBytes, randomUUID } from "node:crypto";

test("a substituted inbox public key or exhausted capacity never offers an upload", async ({
  page,
}) => {
  const id = randomUUID(),
    key = randomBytes(32).toString("base64url");
  let changedKey = true;
  await page.route(`**/api/v1/slots/${id}/availability`, (route) =>
    route.fulfill({
      json: {
        id,
        receive_protocol: 2,
        recipient_public_key: changedKey ? randomBytes(32).toString("base64url") : key,
        available: false,
        remaining_files: 0,
      },
    }),
  );
  await page.goto(`/u/${id}#v2.${key}`);
  await expect(page.getByRole("alert")).toContainText("does not match this inbox");
  await expect(page.getByLabel("Choose files")).toHaveCount(0);
  changedKey = false;
  await page.reload();
  await expect(page.getByRole("alert")).toContainText("cannot accept more files");
  await expect(page.getByLabel("Choose files")).toHaveCount(0);
});

for (const fragment of ["", "#invalid-key"]) {
  test(`incomplete or malformed receive key cannot offer an upload (${fragment || "missing"})`, async ({
    page,
  }) => {
    let requests = 0;
    await page.route("**/api/v1/slots/**", (route) => {
      requests++;
      return route.fulfill({ json: { transfers: [] } });
    });
    await page.goto(`/u/${randomUUID()}${fragment}`);
    await expect(page.getByRole("alert")).toContainText(/Ask (the sender|its owner)/);
    await expect(page.getByRole("button", { name: "Reconnect" })).toHaveCount(0);
    await expect(page.getByLabel("Choose files")).toHaveCount(0);
    expect(requests).toBe(0);
  });
}

test("revoked receive links request a new link while connection errors offer recovery", async ({
  page,
}) => {
  const id = randomUUID(),
    key = randomBytes(32).toString("base64url");
  let responseStatus = 404;
  await page.route(`**/api/v1/slots/${id}/availability`, (route) =>
    route.fulfill({
      status: responseStatus,
      json: {
        id,
        receive_protocol: 2,
        recipient_public_key: key,
        available: true,
        remaining_files: null,
      },
    }),
  );
  await page.goto(`/u/${id}#v2.${key}`);
  await expect(page.getByRole("alert")).toContainText("expired or was revoked");
  await expect(page.getByRole("button", { name: "Reconnect" })).toHaveCount(0);
  responseStatus = 503;
  await page.reload();
  await expect(page.getByRole("alert")).toContainText("Could not connect");
  responseStatus = 200;
  await page.getByRole("button", { name: "Reconnect" }).click();
  await expect(page.getByLabel("Choose files")).toBeVisible();
});
