import { test, expect } from "@playwright/test";
import QRCode from "qrcode";
const id = "01234567-89ab-cdef-0123-456789abcdef",
  key = "A".repeat(43);
async function session(page: import("@playwright/test").Page, signedIn = true) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      status: signedIn ? 200 : 401,
      json: signedIn
        ? { user: { id: "scanner-user", username: "scanner", role: "user" } }
        : { error: "unauthorized" },
    }),
  );
  await page.route("**/api/v1/auth/resources?*", (route) =>
    route.fulfill({ json: { transfers: [], slots: [] } }),
  );
  await page.route("**/api/v1/auth/status", (route) =>
    route.fulfill({ json: { setup_required: false } }),
  );
  await page.addInitScript(() => {
    (window as any).cameraRequests = 0;
    navigator.mediaDevices.getUserMedia = async () => {
      (window as any).cameraRequests++;
      throw new DOMException("Denied", "NotAllowedError");
    };
  });
}
test("direct signed-out scanner URL has no scanner or permission request", async ({ page }) => {
  await session(page, false);
  await page.goto("/?view=scan");
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await expect(page.getByLabel("QR camera preview")).toHaveCount(0);
  expect(await page.evaluate(() => (window as any).cameraRequests)).toBe(0);
});
test("signed-in scan requests camera once; denial keeps paste and images usable", async ({
  page,
}) => {
  await session(page);
  await page.goto("/?view=scan");
  await expect(page.getByRole("alert")).toContainText("Camera access was denied");
  expect(await page.evaluate(() => (window as any).cameraRequests)).toBe(1);
  await page.getByLabel("Paste link", { exact: true }).fill(`https://other.example/d/${id}#${key}`);
  await page.getByRole("button", { name: "Review link" }).click();
  await expect(page.getByText("https://other.example", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open download link" })).toBeVisible();
  expect(page.url()).toContain("?view=scan");
  await page.getByLabel("Paste link", { exact: true }).fill("javascript:alert(1)");
  await page.getByRole("button", { name: "Review link" }).click();
  await expect(page.getByRole("alert")).toContainText("not a supported");
});
test("local PNG decoding uses worker fallback and pairing never redeems", async ({ page }) => {
  await session(page);
  await page.addInitScript(() => {
    delete (window as any).BarcodeDetector;
  });
  await page.goto("/?view=scan");
  const requests: string[] = [];
  page.on("request", (request) => requests.push(request.url()));
  const payload = `https://other.example/u/${id}#v2.${key}`;
  await page.getByLabel("Choose QR image", { exact: true }).setInputFiles({
    name: "qr.png",
    mimeType: "image/png",
    buffer: await QRCode.toBuffer(payload, { width: 600 }),
  });
  await expect(page.getByRole("button", { name: "Open receive link" })).toBeVisible();
  expect(requests.some((url) => url.includes(key) || url.includes("other.example"))).toBe(false);
  const pairing = JSON.stringify({
    type: "psst-pairing",
    version: 1,
    server_url: "https://other.example",
    code: "B".repeat(32),
  });
  await page.getByLabel("Paste link", { exact: true }).fill(pairing);
  await page.getByRole("button", { name: "Review link" }).click();
  await expect(page.getByRole("status")).toContainText("belongs in the mobile app");
  expect(requests.some((url) => url.includes("pairings"))).toBe(false);
  await expect(page.getByRole("button", { name: /Open .* link/ })).toHaveCount(0);
});
test("session expiry removes scanner and blocks opening a retained result", async ({ page }) => {
  await session(page);
  await page.goto("/?view=scan");
  await page.getByLabel("Paste link", { exact: true }).fill(`https://other.example/d/${id}#${key}`);
  await page.getByRole("button", { name: "Review link" }).click();
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ status: 401, json: { error: "expired" } }),
  );
  await page.getByRole("button", { name: "Open download link" }).click();
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await expect(page.getByLabel("QR camera preview")).toHaveCount(0);
  expect(page.url()).not.toContain("other.example");
});
test("LAN HTTP fallback does not request camera", async ({ page }) => {
  await session(page);
  await page.addInitScript(() =>
    Object.defineProperty(window, "isSecureContext", { value: false }),
  );
  await page.goto("/?view=scan");
  await expect(
    page.getByText("Camera scanning needs HTTPS or localhost.", { exact: false }),
  ).toBeVisible();
  expect(await page.evaluate(() => (window as any).cameraRequests)).toBe(0);
  await expect(page.getByLabel("Paste link", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Choose QR image", { exact: true })).toBeVisible();
});
test("active stream stops on navigation and sign-out in another tab", async ({ page }) => {
  await session(page);
  await page.addInitScript(() => {
    navigator.mediaDevices.getUserMedia = async () => {
      const canvas = document.createElement("canvas");
      canvas.width = 320;
      canvas.height = 240;
      const stream = canvas.captureStream();
      setTimeout(() => canvas.getContext("2d")!.fillRect(0, 0, 320, 240), 30);
      (window as any).testStream = stream;
      return stream;
    };
    navigator.mediaDevices.enumerateDevices = async () => [];
  });
  await page.goto("/?view=scan");
  await expect(page.getByRole("button", { name: "Stop camera" })).toBeVisible();
  await page.getByRole("link", { name: "History", exact: true }).click();
  await expect
    .poll(() => page.evaluate(() => (window as any).testStream.getTracks()[0].readyState))
    .toBe("ended");
  await page.getByRole("link", { name: "Scan QR code", exact: true }).click();
  await expect(page.getByRole("button", { name: "Stop camera" })).toBeVisible();
  await page.evaluate(() =>
    window.dispatchEvent(new StorageEvent("storage", { key: "psst.auth-change" })),
  );
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => (window as any).testStream.getTracks()[0].readyState))
    .toBe("ended");
});

test("branded download, upload and long pairing codes decode through the browser fallback", async ({
  page,
}) => {
  await session(page, false);
  await page.goto("/");
  const inputs = [
    `https://files.example/d/${id}#${key}`,
    `https://files.example/u/${id}#v2.${key}`,
    JSON.stringify({
      type: "psst-pairing",
      version: 1,
      server_url: `https://${"long-server-name-".repeat(3)}example.test:8443`,
      code: "aB9_".repeat(32),
    }),
  ];
  const results = await page.evaluate(async (values) => {
    const rendererPath = "/src/lib/branded-qr.ts";
    const { brandedQr } = await import(rendererPath);
    const decoderPath = "/node_modules/qr-scanner/qr-scanner.min.js";
    const { default: Scanner } = await import(decoderPath);
    delete (window as any).BarcodeDetector;
    const results = [];
    for (const value of values) {
      const image = await brandedQr(value);
      const decoded = await Scanner.scanImage(image, { returnDetailedScanResult: true });
      results.push(decoded.data);
    }
    return results;
  }, inputs);
  expect(results).toEqual(inputs);
});

test("administrator can change the server file cap; ordinary accounts cannot see its form", async ({
  page,
}) => {
  await session(page);
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({
      json: { max_file_size: 25 * 1024 ** 2, max_file_size_ceiling: 5 * 1024 ** 3 },
    }),
  );
  await page.goto("/?view=settings");
  await expect(page.getByLabel("Maximum file size (MiB)")).toHaveCount(0);
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: { id: "admin", username: "admin", role: "admin" } } }),
  );
  let saved = 0;
  await page.route("**/api/v1/admin/settings", async (route) => {
    expect(route.request().method()).toBe("PATCH");
    saved = route.request().postDataJSON().max_file_size;
    await route.fulfill({ json: { max_file_size: saved } });
  });
  await page.goto("/?view=server");
  await expect(page.getByLabel("Maximum file size (MiB)")).toHaveValue("25");
  await page.getByLabel("Maximum file size (MiB)").fill("512");
  await page.getByRole("button", { name: "Save file limit" }).click();
  await expect(page.getByRole("status")).toContainText("File limit saved");
  expect(saved).toBe(512 * 1024 ** 2);
});
