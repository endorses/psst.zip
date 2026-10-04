import { test } from "node:test";
import assert from "node:assert/strict";
import {
  validateSlotAvailability,
  assertGuestKey,
  assertGuestSelection,
  selectionWireSize,
  guestManifestWireSize,
  fileManifestEntry,
} from "../src/lib/guest-capacity.ts";
import { generateKey, encryptManifest } from "../src/lib/crypto.ts";
import { encodeReceiveEnvelope } from "../src/lib/receive-crypto.ts";
import { guestAvailability, guestSlotID, guestPublicKey } from "./guest-capacity-fixture.ts";

test("guest capacity is bounded and missing or contradictory fields never become unlimited", () => {
  const v = guestAvailability();
  assert.equal(validateSlotAvailability(v, guestSlotID), v);
  for (const change of [
    undefined,
    null,
    {},
    { ...v.upload_capacity, available_files: 101 },
    { ...v.upload_capacity, available_wire_bytes: 2 ** 50 + 1 },
    { ...v.upload_capacity, available_wire_bytes: 59 },
    { ...v.upload_capacity, available_files: 2, available_wire_bytes: 60 },
    { ...v.upload_capacity, manifest_reserve_bytes: 0 },
    { ...v.upload_capacity, manifest_reserve_bytes: 1048577 },
    { ...v.upload_capacity, checked_at: "/private/error" },
    { ...v.upload_capacity, state: "unknown", reason: "capacity_unavailable" },
    { ...v.upload_capacity, state: "blocked", available_files: 0, available_wire_bytes: 0 },
  ])
    assert.throws(
      () => validateSlotAvailability({ ...v, upload_capacity: change }, guestSlotID),
      /Could not check upload space/,
    );
  assert.throws(() => validateSlotAvailability({ ...v, remaining_bytes: -1 }, guestSlotID));
  assert.throws(() => validateSlotAvailability({ ...v, remaining_files: 1 }, guestSlotID));
  assert.throws(() => validateSlotAvailability({ ...v, max_files: 1 }, guestSlotID));
  assert.throws(() =>
    validateSlotAvailability({ ...v, max_files: 1, remaining_files: 2 }, guestSlotID),
  );
  assert.throws(() => validateSlotAvailability(v, "different-inbox"));
  assert.doesNotThrow(() => assertGuestKey(v, guestPublicKey));
  assert.throws(() => assertGuestKey(v, "other-public-key"), /does not match/);
});

test("aggregate capacity uses encrypted empty-file overhead and cumulative link guards", () => {
  const v = guestAvailability();
  v.upload_capacity.available_files = 2;
  v.upload_capacity.available_wire_bytes = 121;
  assert.equal(assertGuestSelection(v, [{ size: 1 }, { size: 0 }], 100), 121);
  assert.throws(() => assertGuestSelection(v, [{ size: 2 }, { size: 0 }], 100), /exceed the space/);
  assert.throws(
    () => assertGuestSelection(v, [{ size: 0 }, { size: 0 }, { size: 0 }], 100),
    /Too many files/,
  );
  assert.throws(
    () => assertGuestSelection({ ...v, remaining_files: 1 }, [{ size: 0 }, { size: 0 }], 100),
    /Too many files/,
  );
  assert.throws(
    () => assertGuestSelection({ ...v, remaining_bytes: 59 }, [{ size: 0 }], 100),
    /exceed the space/,
  );
  assert.throws(
    () => assertGuestSelection({ ...v, remaining_transfers: 0 }, [], 100),
    /upload allowance/,
  );
  assert.throws(() => selectionWireSize([{ size: Number.MAX_SAFE_INTEGER }]), /Files must/);
  assert.throws(
    () => selectionWireSize(Array.from({ length: 101 }, () => ({ size: 0 }))),
    /100 files/,
  );
  assert.throws(() => assertGuestSelection(v, [{ size: 2 }], 1), /Files must/);
});

test("unknown and exhausted snapshots have distinct retry guidance", () => {
  const v = guestAvailability();
  const unknown = {
    ...v,
    upload_capacity: {
      ...v.upload_capacity,
      state: "unknown",
      reason: "capacity_unavailable",
      available_wire_bytes: null,
      available_files: null,
    },
  };
  const checked = validateSlotAvailability(unknown, guestSlotID);
  assert.throws(() => assertGuestSelection(checked, [], 100), /Refresh availability/);
  for (const reason of ["capacity_limit", "link_limit"]) {
    const blocked = validateSlotAvailability(
      {
        ...v,
        upload_capacity: {
          ...v.upload_capacity,
          state: "blocked",
          reason,
          available_wire_bytes: 0,
          available_files: 0,
        },
      },
      guestSlotID,
    );
    assert.throws(
      () => assertGuestSelection(blocked, [], 100),
      reason === "capacity_limit" ? /currently unavailable/ : /new link/,
    );
  }
});

test("guest manifest reserve matches actual encrypted UTF-8 metadata and rejects one byte over", async () => {
  const files = [
    { name: 'quote"雪.txt', size: 0, type: "" },
    { name: "second.bin", size: 9, type: "text/plain" },
  ];
  const encrypted = await encryptManifest(await generateKey(), {
    files: files.map((file) =>
      fileManifestEntry(file, "12345678-1234-4234-8234-123456789012", "a".repeat(32)),
    ),
  });
  const actual = encodeReceiveEnvelope(new Uint8Array(80), new Uint8Array(encrypted));
  assert.equal(guestManifestWireSize(files), actual.length);
  const v = guestAvailability();
  v.upload_capacity.manifest_reserve_bytes = actual.length;
  assert.doesNotThrow(() => assertGuestSelection(v, files, 100));
  v.upload_capacity.manifest_reserve_bytes--;
  assert.throws(() => assertGuestSelection(v, files, 100), /file details exceed/);
});

test("stale and future capacity snapshots cannot authorize allocation", () => {
  const v = guestAvailability();
  for (const offset of [-120001, 125000]) {
    v.upload_capacity.checked_at = new Date(Date.now() + offset).toISOString();
    assert.throws(() => assertGuestSelection(v, [{ size: 0 }], 100), /out of date/);
  }
});
