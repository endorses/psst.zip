import { test, expect, signIn } from "./auth-fixture";

test("a server that ignores selected download policy cannot publish a send link", async ({
  page,
  request,
}) => {
  await signIn(page);
  let id = "",
    fileRequests = 0;
  await page.route("**/api/v1/transfers/*", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    const response = await route.fetch();
    const body = await response.json();
    id = body.id;
    delete body.max_downloads;
    await route.fulfill({ response, json: body });
  });
  page.on("request", (r) => {
    if (r.url().endsWith("/files") && r.method() === "POST") fileRequests++;
  });
  await page
    .getByLabel("Choose files")
    .setInputFiles({ name: "secret.txt", mimeType: "text/plain", buffer: Buffer.from("private") });
  await page.getByRole("checkbox", { name: "Limit downloads per file" }).check();
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("server did not accept the download limit");
  await expect(page.getByLabel("Full link")).toHaveCount(0);
  expect(fileRequests).toBe(0);
  expect(id).not.toBe("");
  expect((await request.get(`/api/v1/transfers/${id}`)).status()).toBe(404);
});

for (const unavailable of [false, true]) {
  test(`receive creation verifies accepted policy before sharing (${unavailable ? "offline inspection" : "unsupported server"})`, async ({
    page,
    request,
  }) => {
    await signIn(page);
    let id = "";
    page.on("response", async (response) => {
      if (response.url().endsWith("/api/v1/slots") && response.request().method() === "POST")
        id = (await response.json()).id;
    });
    await page.route(/\/api\/v1\/slots\/[^/]+\/inbox(?:\?|$)/, async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      if (unavailable) return route.abort();
      const response = await route.fetch();
      const body = await response.json();
      // A supported page shape with a legacy protocol must still be rejected
      // before publishing an invitation. Owner verification now uses inbox pages.
      body.receive_protocol = 1;
      body.recipient_public_key = "";
      await route.fulfill({ response, json: body });
    });
    await page.getByRole("link", { name: "Receive", exact: true }).click();
    await page.getByRole("button", { name: "Create receive link", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText(
      unavailable
        ? "Could not verify the new inbox"
        : "server did not accept the private inbox protocol",
    );
    await expect(page.getByLabel("Full link")).toHaveCount(0);
    await expect.poll(() => id).not.toBe("");
    expect((await request.get(`/api/v1/slots/${id}`)).status()).toBe(unavailable ? 200 : 404);
    if (unavailable) {
      await page.getByRole("link", { name: "History", exact: true }).click();
      await expect(page.locator(`[data-resource-id="${id}"]`)).toBeVisible();
      expect(
        await page.evaluate(
          (slot) =>
            Object.keys(localStorage).some(
              (key) => key.startsWith("psst.receive-key.v2.") && key.endsWith(slot),
            ),
          id,
        ),
      ).toBe(true);
    }
  });
}

test("HTTP pages explain the client delivery trust boundary without absolute safety claims", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page.getByRole("note")).toContainText("This connection uses HTTP");
  await expect(page.locator("footer")).toContainText(
    "Encryption does not verify the sender or make a file safe",
  );
  await expect(page.locator("footer")).not.toContainText("never readable");
});
