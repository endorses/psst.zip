import assert from "node:assert/strict";
import { test } from "node:test";
import {
  storeReceiveKey,
  loadReceiveKey,
  removeReceiveKey,
  parseReceiveFragment,
} from "../src/lib/receive-keys.ts";
import { validateLinkLimit } from "../src/lib/link-limits.ts";

test("receive private keys are owner/inbox scoped and corrupt or unavailable storage fails closed", () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  const records = new Map<string, string>();
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem: (name: string) => records.get(name) ?? null,
      setItem: (name: string, value: string) => records.set(name, value),
      removeItem: (name: string) => records.delete(name),
    },
  });
  try {
    const pair = {
      publicKey: new Uint8Array(32).fill(17),
      privateKey: new Uint8Array(32).fill(42),
    };
    storeReceiveKey("alice", "inbox1", pair);
    assert.deepEqual(loadReceiveKey("alice", "inbox1"), pair);
    assert.equal(loadReceiveKey("bob", "inbox1"), null);
    assert.equal(loadReceiveKey("alice", "inbox2"), null);
    removeReceiveKey("bob", "inbox1");
    assert.deepEqual(loadReceiveKey("alice", "inbox1"), pair);
    records.set("psst.receive-key.v2.alice.inbox1", '{"privateKey":"corrupt"}');
    assert.equal(loadReceiveKey("alice", "inbox1"), null);
    records.set("psst.receive-key.v2.alice.inbox1", " ".repeat(513));
    assert.equal(loadReceiveKey("alice", "inbox1"), null);
    localStorage.setItem = () => {
      throw new Error("storage unavailable");
    };
    assert.throws(() => storeReceiveKey("alice", "inbox1", pair), /storage unavailable/);
  } finally {
    if (original) Object.defineProperty(globalThis, "localStorage", original);
    else Reflect.deleteProperty(globalThis, "localStorage");
  }
});

test("receive invitations require canonical versioned public keys and link limits reject invalid numbers", () => {
  const encoded = Buffer.alloc(32, 17).toString("base64url");
  assert.deepEqual(parseReceiveFragment(`v2.${encoded}`).publicKey, new Uint8Array(32).fill(17));
  for (const invalid of [
    encoded,
    `v1.${encoded}`,
    `v2.${encoded}=`,
    `v2.${encoded.slice(0, -1)}F`,
    `v2.${encoded}/`,
  ])
    assert.throws(() => parseReceiveFragment(invalid));
  for (const value of [0, 1, 2147483647]) assert.doesNotThrow(() => validateLinkLimit(value));
  for (const value of [-1, 0.5, 2147483648, NaN, Infinity])
    assert.throws(() => validateLinkLimit(value));
});
