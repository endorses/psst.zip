export const historyID = (index: number) =>
  `11111111-1111-4111-8111-${String(index).padStart(12, "0")}`;
export const historyCursor = (index: number) =>
  Buffer.from(JSON.stringify({ page: index })).toString("base64url");
export function historyTransfer(overrides: Record<string, unknown> = {}) {
  const count = overrides.file_count === undefined ? 1 : overrides.file_count;
  const size = overrides.total_size === undefined ? 60 : overrides.total_size;
  const completed = count === null ? null : overrides.status === "pending" ? 0 : count;
  return {
    id: historyID(1),
    status: "complete",
    created_at: "2026-01-01T00:00:00Z",
    expires_at: "2099-01-01T00:00:00Z",
    files: [],
    file_count: count,
    total_size: size,
    has_manifest: true,
    max_downloads: 0,
    download_count: 0,
    downloaded_at: null,
    summary: {
      state: count === null ? "updating" : "ready",
      completed_files: completed,
      file_count: count,
      total_size: size,
    },
    ...overrides,
  };
}
export function historySlot(overrides: Record<string, unknown> = {}) {
  const count = overrides.file_count === undefined ? 1 : overrides.file_count;
  const completed = overrides.completed_files === undefined ? count : overrides.completed_files;
  const size = overrides.total_size === undefined ? 60 : overrides.total_size;
  return {
    id: historyID(2),
    status: "waiting",
    created_at: "2026-01-01T00:00:00Z",
    expires_at: "2099-01-01T00:00:00Z",
    transfers: [],
    receive_protocol: 2,
    recipient_public_key: "A".repeat(43),
    max_files: 0,
    reserved_files: 1,
    remaining_files: null,
    file_count: count,
    completed_files: completed,
    total_size: size,
    summary: {
      state: count === null ? "updating" : "ready",
      completed_files: completed,
      file_count: count,
      total_size: size,
    },
    ...overrides,
  };
}
export function historyPage(overrides: Record<string, unknown> = {}) {
  return { transfers: [], slots: [], paginated: true, next_cursor: null, ...overrides };
}
