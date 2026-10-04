import { test, expect, signIn } from "./auth-fixture";

for (const viewport of [
  { width: 1280, height: 720 },
  { width: 390, height: 844 },
]) {
  test(`history revoke confirmation stays in view and restores focus at ${viewport.width}px`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    await page.emulateMedia({ colorScheme: viewport.width < 500 ? "dark" : "light" });
    let slots = Array.from({ length: 30 }, (_, index) => ({
      id: `slot-${index}`,
      status: "waiting",
      expires_at: new Date(Date.now() + 86400000).toISOString(),
      transfers: [],
    }));
    await page.route("**/api/v1/auth/resources?*", (route) =>
      route.fulfill({ json: { transfers: [], slots } }),
    );
    let deletes = 0;
    let release: () => void = () => {};
    const pending = new Promise<void>((resolve) => (release = resolve));
    await page.route("**/api/v1/slots/slot-15", async (route) => {
      expect(route.request().method()).toBe("DELETE");
      deletes++;
      if (deletes === 1) {
        await pending;
        await route.fulfill({ status: 503, json: { error: "Please retry revocation." } });
      } else {
        slots = slots.filter((slot) => slot.id !== "slot-15");
        await route.fulfill({ status: 204 });
      }
    });
    await signIn(page);
    await page.getByRole("link", { name: "History", exact: true }).click();
    const row = page.locator('[data-resource-id="slot-15"]');
    const opener = row.getByRole("button", { name: "Revoke", exact: true });
    await opener.scrollIntoViewIfNeeded();
    await opener.click();
    const scroll = await page.evaluate(() => scrollY);
    const dialog = page.getByRole("dialog", { name: "Revoke this link?" });
    await expect(dialog).toBeVisible();
    const bounds = (await dialog.boundingBox())!;
    expect(bounds.y).toBeGreaterThanOrEqual(0);
    expect(bounds.y + bounds.height).toBeLessThanOrEqual(viewport.height);
    const cancel = dialog.getByRole("button", { name: "Cancel", exact: true });
    const confirm = dialog.getByRole("button", { name: "Revoke and delete", exact: true });
    await expect(cancel).toBeFocused();
    await page.screenshot({ path: test.info().outputPath("revoke-confirmation.png") });
    await page.keyboard.press("Tab");
    await expect(confirm).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(cancel).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(opener).toBeFocused();
    expect(await page.evaluate(() => scrollY)).toBe(scroll);
    expect(deletes).toBe(0);
    await opener.click();
    await cancel.click();
    await expect(opener).toBeFocused();
    expect(deletes).toBe(0);
    await opener.click();
    await confirm.click();
    await expect(dialog.getByRole("button", { name: "Revoking…" })).toBeDisabled();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeVisible();
    release();
    await expect(dialog.getByRole("alert")).toContainText("Please retry revocation.");
    await confirm.click();
    await expect(dialog).toHaveCount(0);
    await expect(row).toHaveCount(0);
    expect(deletes).toBe(2);
  });
}
