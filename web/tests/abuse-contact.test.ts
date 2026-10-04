import { test } from "node:test";
import assert from "node:assert/strict";
import {
  abuseMailto,
  abuseReference,
  loadAbuseContact,
  validAbuseEmail,
} from "../src/lib/abuse-contact.ts";

test("abuse contact accepts a single bounded address and rejects header/URL injection", () => {
  for (const value of [
    "abuse@example.com",
    "ops+reports@files.example.org",
    "a_b.c-d%tag@example.com",
  ])
    assert.ok(validAbuseEmail(value));
  for (const value of [
    null,
    "",
    "x@example.com\r\nBcc: other@example.com",
    "x@example.com?subject=oops",
    "mailto:x@example.com",
    "Display <x@example.com>",
    "a@b.com,c@d.com",
    ".a@b.com",
    "a..b@c.com",
    "a@localhost",
    "a@-example.com",
    "a@ex ample.com",
    "a".repeat(65) + "@example.com",
    "a@" + "b".repeat(64) + ".com",
    "a@éxample.com",
  ])
    assert.equal(validAbuseEmail(value), false, String(value));
});

test("report references and mail drafts contain only instance and validated opaque identity", () => {
  const id = "59b91455-7313-42a4-b8ce-58294ceb42ba";
  assert.equal(
    abuseReference("https://files.example.com", `/d/${id}`),
    `Instance: https://files.example.com\nResource type: transfer\nResource ID: ${id}`,
  );
  assert.match(abuseReference("https://files.example.com", `/u/${id}`), /Resource type: slot/);
  for (const origin of [
    "https://user:secret@example.com",
    "https://example.com/#secret",
    "https://example.com?secret",
    "javascript:alert(1)",
  ])
    assert.throws(() => abuseReference(origin));
  for (const path of [
    `/d/${id}#private-key`,
    `/u/${id}?token=secret`,
    "/d/secret-file-name",
    "/account",
  ])
    assert.equal(
      abuseReference("http://192.168.1.2:8080", path),
      "Instance: http://192.168.1.2:8080",
    );
  const draft = new URL(
    abuseMailto("abuse+reports@example.com", "https://files.example.com", `/u/${id}`),
  );
  assert.equal(decodeURIComponent(draft.pathname), "abuse+reports@example.com");
  assert.deepEqual([...draft.searchParams.keys()], ["subject", "body"]);
  assert.match(draft.searchParams.get("body")!, /Resource ID: 59b91455/);
  assert.throws(() => abuseMailto("a@b.com?bcc=bad", "https://example.com"));
});

test("public contact fetch omits credentials and redirect/referrer and bounds response", async () => {
  const original = globalThis.fetch;
  const signal = new AbortController().signal;
  try {
    globalThis.fetch = async (input, init) => {
      assert.equal(input, "/api/v1/config");
      assert.equal(init?.credentials, "omit");
      assert.equal(init?.redirect, "error");
      assert.equal(init?.referrerPolicy, "no-referrer");
      assert.equal(init?.signal, signal);
      return Response.json({ abuse_contact_email: "abuse@example.com" });
    };
    assert.equal(await loadAbuseContact(signal), "abuse@example.com");
    globalThis.fetch = async () => Response.json({});
    assert.equal(await loadAbuseContact(signal), "");
    globalThis.fetch = async () =>
      Response.json({ abuse_contact_email: "x@example.com\nBcc: secret" });
    assert.equal(await loadAbuseContact(signal), "");
    globalThis.fetch = async () => new Response(" ".repeat(65537));
    await assert.rejects(loadAbuseContact(signal), /too large/);
    globalThis.fetch = async () => new Response(null, { status: 503 });
    await assert.rejects(loadAbuseContact(signal), /unavailable/);
  } finally {
    globalThis.fetch = original;
  }
});
