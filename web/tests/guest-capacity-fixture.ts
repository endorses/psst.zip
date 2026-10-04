import type { SlotAvailability } from "../src/lib/guest-capacity.ts";
export const guestSlotID = "55555555-5555-4555-8555-555555555555";
export const guestPublicKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE";
export function guestAvailability(overrides: Partial<SlotAvailability> = {}): SlotAvailability {
  return {
    id: guestSlotID,
    receive_protocol: 2,
    recipient_public_key: guestPublicKey,
    max_files: 0,
    remaining_files: null,
    remaining_bytes: 1024 ** 3,
    remaining_transfers: 20,
    available: true,
    upload_capacity: {
      checked_at: new Date().toISOString(),
      state: "ready",
      available_files: 100,
      available_wire_bytes: 1024 ** 3,
      manifest_reserve_bytes: 1048576,
    },
    ...overrides,
  };
}
