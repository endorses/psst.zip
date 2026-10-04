export const resourcePolicy = {
  server_storage_bytes: 10 * 1024 ** 3,
  account_storage_bytes: 2 * 1024 ** 3,
  server_files: 10000,
  account_files: 1000,
  server_transfers: 2000,
  account_transfers: 200,
  server_slots: 500,
  account_slots: 50,
  max_retention_seconds: 604800,
  pending_upload_seconds: 86400,
  reserve_disk_bytes: 268435456,
  reserve_disk_percent: 5,
};
export const resourceUsage = {
  reserved_bytes: 1024 ** 3,
  occupied_bytes: 1024 ** 2,
  files: 8,
  transfers: 3,
  slots: 2,
};
