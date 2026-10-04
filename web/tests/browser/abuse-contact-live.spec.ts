import { test, expect, authenticate, adminCredentials } from "./auth-fixture";

test("persisted administrator contact reaches an anonymous recipient without link secrets", async ({
  page,
  browser,
  adminRequest,
}) => {
  await page.goto("/?view=server");
  await authenticate(page, adminCredentials);
  await page.getByRole("link", { name: "Server settings", exact: true }).click();
  const contact = page.getByLabel("Public contact email (optional)");
  await contact.fill("reports+browser@example.com");
  await page.getByRole("button", { name: "Save abuse contact", exact: true }).click();
  await expect(page.getByText("Abuse contact published.", { exact: true })).toBeVisible();
  await page.reload();
  await expect(contact).toHaveValue("reports+browser@example.com");
  const guest = await browser.newContext();
  try {
    const recipient = await guest.newPage();
    const id = "59b91455-7313-42a4-b8ce-58294ceb42ba";
    await recipient.goto(`http://127.0.0.1:4173/u/${id}#v2.invalid-secret`);
    await recipient.getByRole("button", { name: "Report abuse", exact: true }).click();
    await expect(recipient.getByLabel("Contact", { exact: true })).toHaveValue(
      "reports+browser@example.com",
    );
    await expect(recipient.getByLabel("Report reference")).toHaveValue(
      `Instance: http://127.0.0.1:4173\nResource type: slot\nResource ID: ${id}`,
    );
    const draft = await recipient
      .getByRole("link", { name: "Open email draft" })
      .getAttribute("href");
    expect(draft).not.toContain("invalid-secret");
  } finally {
    await guest.close();
    expect(
      (await adminRequest.patch("/api/v1/admin/abuse-contact", { data: { email: "" } })).ok(),
    ).toBe(true);
  }
});
