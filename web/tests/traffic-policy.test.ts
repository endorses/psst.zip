import assert from "node:assert/strict";
import { test } from "node:test";
import { trafficPolicy, trafficSnapshot } from "./traffic-policy-fixture.ts";
import {
  TrafficLimitError,
  trafficLimitError,
  validateTrafficPolicy,
  validateTrafficSnapshot,
  validateAccountTrafficSnapshot,
} from "../src/lib/traffic-policy.ts";
import { uploadEncryptedFile } from "../src/lib/stream-upload.ts";

test("traffic policy rejects ambiguous enablement, unsafe bytes and out-of-range controls", () => {
  assert.equal(validateTrafficPolicy(trafficPolicy).enforcement_enabled, false);
  for (const patch of [
    { enforcement_enabled: "false" },
    { server_budget_bytes: 0 },
    { default_account_budget_bytes: 1.5 },
    { server_budget_bytes: Number.MAX_SAFE_INTEGER + 1 },
    { cycle_start_day: 32 },
    { max_streams_per_ip: 4097 },
    { upload_bytes_per_second: 10737418241 },
    { basis: "total" },
  ])
    assert.throws(() => validateTrafficPolicy({ ...trafficPolicy, ...patch }));
  assert.equal(
    validateTrafficPolicy({
      ...trafficPolicy,
      enforcement_enabled: true,
      server_budget_bytes: Number.MAX_SAFE_INTEGER,
    }).enforcement_enabled,
    true,
  );
});
test("traffic status preserves separate conservative and reservation charges and explicit account inheritance", () => {
  const snapshot = trafficSnapshot();
  assert.equal(validateTrafficSnapshot(snapshot).usage.charged_bytes, 1200);
  assert.throws(() =>
    validateTrafficSnapshot({
      ...snapshot,
      usage: { ...snapshot.usage, reserved_downloaded_bytes: -1 },
    }),
  );
  for (const invalid of [
    { recording_started_at: "unknown" },
    { cycle: { ...snapshot.cycle, end: snapshot.cycle.start } },
    { lease_bytes: 65537 },
    { usage: { ...snapshot.usage, remaining_bytes: snapshot.usage.budget_bytes + 1 } },
  ])
    assert.throws(() => validateTrafficSnapshot({ ...snapshot, ...invalid }));
  const account = {
    ...snapshot,
    account_budget_bytes: null,
    effective_budget_bytes: trafficPolicy.default_account_budget_bytes,
  };
  assert.equal(validateAccountTrafficSnapshot(account).account_budget_bytes, null);
  assert.throws(() => validateAccountTrafficSnapshot({ ...account, account_budget_bytes: 0 }));
});
test("traffic failures use safe copy and only valid retry timestamps", () => {
  assert.equal(trafficLimitError("unknown"), null);
  assert.match(trafficLimitError("traffic_policy_changed")!.message, /Saved files are safe/);
  assert.equal(
    trafficLimitError("traffic_budget_exhausted", "2026-11-01T00:00:00." + "0".repeat(100) + "Z")
      ?.retryAt,
    null,
  );
  assert.equal(trafficLimitError("traffic_budget_exhausted", "<script>")?.retryAt, null);
  assert.match(
    trafficLimitError("traffic_budget_exhausted", "2026-11-01T00:00:00Z")!.message,
    /2026-11-01 00:00:00 UTC/,
  );
  assert.match(trafficLimitError("traffic_accounting_unavailable")!.message, /retry manually/);
});
for (const code of [
  "traffic_budget_exhausted",
  "traffic_accounting_unavailable",
  "traffic_policy_changed",
] as const)
  for (const termination of ["json", "header", "network"] as const) {
    if (code === "traffic_policy_changed" && termination === "network") continue;
    test(`${code} ${termination} stops upload before HEAD or another payload request`, async () => {
      const previousFetch = globalThis.fetch,
        previousWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
      Object.defineProperty(globalThis, "window", {
        configurable: true,
        value: { location: { origin: "https://example.test" } },
      });
      const requests: string[] = [];
      try {
        globalThis.fetch = async (input, init) => {
          requests.push(init?.method ?? "GET");
          assert.equal(init?.credentials, "omit");
          if (String(input).includes("/traffic-status?direction=upload")) {
            assert.equal(
              new Headers(init?.headers).get("Authorization"),
              "Bearer public-test-only",
            );
            return Response.json({
              state: code === "traffic_budget_exhausted" ? "exhausted" : "unavailable",
              retry_at: "2026-11-01T00:00:00Z",
            });
          }
          if (init?.method === "POST")
            return new Response(null, {
              status: 201,
              headers: {
                Location: "/api/v1/transfers/transfer/files/12345678-1234-1234-1234-123456789012",
              },
            });
          if (termination === "network") throw new TypeError("stream terminated");
          return termination === "header"
            ? new Response(null, {
                status: 503,
                headers: { "X-Psst-Error-Code": code, "X-Psst-Retry-At": "2026-11-01T00:00:00Z" },
              })
            : Response.json({ code, retry_at: "2026-11-01T00:00:00Z" }, { status: 429 });
        };
        await assert.rejects(
          uploadEncryptedFile({
            key: new Uint8Array(32).fill(7),
            file: new Blob(["one"]),
            encryptionId: "12".repeat(16),
            endpoint: "/api/v1/transfers/transfer/files",
            token: "public-test-only",
            signal: new AbortController().signal,
            onProgress: () => {},
          }),
          (error) => error instanceof TrafficLimitError && error.code === code,
        );
        assert.deepEqual(
          requests,
          termination === "network" ? ["POST", "PATCH", "GET"] : ["POST", "PATCH"],
        );
      } finally {
        globalThis.fetch = previousFetch;
        if (previousWindow) Object.defineProperty(globalThis, "window", previousWindow);
        else Reflect.deleteProperty(globalThis, "window");
      }
    });
  }
