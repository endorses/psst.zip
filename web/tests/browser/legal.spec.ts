import { test, expect } from "@playwright/test";

test("source and licenses are discoverable without signing in and identify the deployed commit", async ({
  page,
}) => {
  const revision = "a".repeat(40);
  await page.route("**/licenses/release.json", (route) =>
    route.fulfill({
      json: {
        name: "psst.zip",
        license: "AGPL-3.0-only",
        version: "v1.2.3",
        revision,
        source: "https://operator.example/source",
        source_archive: `https://operator.example/source/archive/${revision}.tar.gz`,
      },
    }),
  );
  await page.goto("/");
  await page.getByRole("link", { name: "Source & licenses", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Source & licenses", exact: true })).toBeVisible();
  await expect(page.getByText(revision, { exact: true })).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Download source for this version" }),
  ).toHaveAttribute("href", `https://operator.example/source/archive/${revision}.tar.gz`);
  await expect(page.getByRole("link", { name: "Project license (AGPL v3)" })).toHaveAttribute(
    "href",
    "/licenses/AGPL-3.0-only.txt",
  );
});

test("missing metadata leaves license access usable without claiming an exact source", async ({
  page,
}) => {
  await page.route("**/licenses/release.json", (route) => route.fulfill({ status: 404 }));
  await page.goto("/legal");
  await expect(page.getByRole("status")).toContainText("Exact source metadata is unavailable");
  await expect(page.getByRole("link", { name: "Download source for this version" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Third-party notices", exact: true })).toBeVisible();
});
