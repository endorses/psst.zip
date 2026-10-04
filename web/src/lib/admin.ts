export interface Totals {
  uploaded_bytes: number;
  downloaded_bytes: number;
  total_bytes: number;
  files_uploaded: number;
  files_delivered: number;
  standalone_files_uploaded: number;
  received_files_uploaded: number;
}
export interface TrafficSettings {
  allowance_bytes: number | null;
  cycle_start_day: number;
  basis: "outbound" | "combined";
}
export interface TrafficReport {
  recording_started_at: string;
  updated_at: string;
  status: "ok" | "degraded";
  timezone: "UTC";
  settings: TrafficSettings;
  today: Totals;
  month: Totals;
  lifetime: Totals;
  range: { from: string; to: string };
  totals: Totals;
  days: ({ date: string } & Totals)[];
  cycle: Totals & {
    start: string;
    end: string;
    counted_bytes: number;
    remaining_bytes: number | null;
  };
}
export interface Overview {
  recording_started_at: string;
  enabled_users: number;
  active_transfers: number;
  active_receive_links: number;
  stored_bytes: number;
  files_uploaded: number;
  files_delivered: number;
  standalone_files_uploaded: number;
  received_files_uploaded: number;
  traffic: TrafficReport;
}
export const measurementExplanation =
  "Application transfer traffic counts encrypted file and manifest bytes, including retries and partial transfers. It excludes HTTP/TLS overhead, assets, backups and other services. Provider billing may differ; transmitted bytes do not prove files were saved.";

export function utcTime(value: string) {
  return new Date(value).toLocaleString(undefined, { timeZone: "UTC" }) + " UTC";
}
