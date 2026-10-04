import assert from "node:assert/strict";
import { test } from "node:test";
import { uploadEncryptedFile } from "../src/lib/stream-upload.ts";
import { TransferStateError, validateIncidentState } from "../src/lib/incident-state.ts";

test("incident state rejects malformed booleans rather than offering an unsafe toggle", () => {
  assert.throws(() =>
    validateIncidentState({ public_transfers_paused: "false", updated_at: "now" }),
  );
  assert.throws(() => validateIncidentState({ updated_at: "now" }));
  assert.equal(
    validateIncidentState({ public_transfers_paused: true, updated_at: "now" })
      .public_transfers_paused,
    true,
  );
});

for (const termination of ["json", "header", "network"] as const) {
  test(`a paused ${termination} upload stops before any HEAD or payload retry`, async () => {
    const originalFetch = globalThis.fetch;
    const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      value: { location: { origin: "https://example.test" } },
    });
    const methods: string[] = [];
    try {
      globalThis.fetch = async (input, init) => {
        assert.equal(init?.credentials, "omit");
        methods.push(init?.method ?? "GET");
        if (String(input) === "/api/v1/config")
          return Response.json({ public_transfers_paused: true });
        if (init?.method === "POST")
          return new Response(null, {
            status: 201,
            headers: {
              Location: "/api/v1/transfers/transfer/files/12345678-1234-1234-1234-123456789012",
            },
          });
        if (termination === "network") throw new TypeError("stream terminated");
        if (termination === "header")
          return new Response(null, {
            status: 503,
            headers: { "X-Psst-Error-Code": "public_transfers_paused" },
          });
        return Response.json({ code: "public_transfers_paused" }, { status: 503 });
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
        (error) => error instanceof TransferStateError && error.code === "public_transfers_paused",
      );
      assert.deepEqual(
        methods,
        termination === "network" ? ["POST", "PATCH", "GET"] : ["POST", "PATCH"],
      );
    } finally {
      globalThis.fetch = originalFetch;
      if (originalWindow) Object.defineProperty(globalThis, "window", originalWindow);
      else Reflect.deleteProperty(globalThis, "window");
    }
  });
}
