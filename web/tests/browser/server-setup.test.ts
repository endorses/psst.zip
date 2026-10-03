import { test, expect } from "@playwright/test";

test("one origin serves the API and both types of share page", async ({ request }) => {
  const health = await request.get("/api/v1/health");
  expect(health.ok()).toBe(true);
  expect(await health.json()).toEqual({ service: "psst.zip", api_version: 1 });

  for (const path of ["/d/_connection_check", "/u/_connection_check"]) {
    const response = await request.get(path);
    expect(response.ok()).toBe(true);
    expect(response.headers()["content-type"]).toContain("text/html");
    expect(await response.text()).toMatch(/<meta\s+name="psst-web"\s+content="1"\s*\/?\s*>/);
  }

  // Missing API routes must not fall back to the website's HTML shell.
  const missing = await request.get("/api/v1/missing-setup-endpoint");
  expect(missing.status()).toBe(404);
  expect(missing.headers()["content-type"]).not.toContain("text/html");
});
