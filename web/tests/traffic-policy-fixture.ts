import type { TrafficPolicy, TrafficSnapshot } from "../src/lib/traffic-policy.ts";
export const trafficPolicy: TrafficPolicy = {
  enforcement_enabled: false,
  server_budget_bytes: 100 * 1024 ** 3,
  default_account_budget_bytes: 10 * 1024 ** 3,
  basis: "outbound",
  cycle_start_day: 1,
  upload_bytes_per_second: 100 * 1024 ** 2,
  download_bytes_per_second: 100 * 1024 ** 2,
  max_active_streams: 64,
  max_streams_per_account: 4,
  max_streams_per_ip: 4,
  max_streams_per_transfer: 4,
  max_streams_per_slot: 4,
};
export function trafficSnapshot(policy: TrafficPolicy = trafficPolicy): TrafficSnapshot {
  return {
    policy: { ...policy },
    state: "ready",
    lease_bytes: 65536,
    recording_started_at: "2026-10-01T00:00:00Z",
    cycle: { start: "2026-10-01T00:00:00Z", end: "2026-11-01T00:00:00Z" },
    usage: {
      observed_uploaded_bytes: 100,
      observed_downloaded_bytes: 200,
      reserved_uploaded_bytes: 300,
      reserved_downloaded_bytes: 400,
      conservative_uploaded_bytes: 500,
      conservative_downloaded_bytes: 600,
      charged_bytes: 1200,
      budget_bytes: policy.server_budget_bytes,
      remaining_bytes: policy.server_budget_bytes - 1200,
    },
  };
}
