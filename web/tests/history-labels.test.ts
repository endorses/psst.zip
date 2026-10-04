import test from "node:test";
import assert from "node:assert/strict";
import {
  labelFor,
  labelKey,
  compactTitle,
  loadLabels,
  saveLabels,
} from "../src/lib/history-labels.ts";
test("labels retain old names, separate resource types and account stores, preserve filename extensions", () => {
  const values = new Map<string, string>();
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem: (k: string) => values.get(k) ?? null,
      setItem: (k: string, v: string) => values.set(k, v),
    },
  });
  const legacy = { same: { title: "Holiday.jpg", size: 12 } };
  saveLabels("a", legacy);
  assert.equal(labelFor(loadLabels("a"), "transfers", "same").title, "Holiday.jpg");
  const changed = { ...loadLabels("a"), [labelKey("slots", "same")]: { custom: "結婚式 💕" } };
  saveLabels("a", changed);
  assert.equal(labelFor(loadLabels("a"), "slots", "same").custom, "結婚式 💕");
  assert.equal(labelFor(loadLabels("a"), "transfers", "same").title, "Holiday.jpg");
  assert.deepEqual(loadLabels("b"), {});
  const name = "😀".repeat(100) + ".jpg + 3 files";
  assert.ok(compactTitle(name).endsWith(".jpg + 3 files"));
  assert.equal(Array.from(compactTitle(name)).length, 72);
});
