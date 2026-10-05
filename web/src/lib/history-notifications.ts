/** Same-origin invalidation hints contain no resource IDs or private material. */
export const HISTORY_CHANNEL = "psst.history-cache.v1";
export function notifyHistoryMutation(): void {
  if (typeof window === "undefined" || typeof BroadcastChannel === "undefined") return;
  const channel = new BroadcastChannel(HISTORY_CHANNEL);
  channel.postMessage({ mutation: true });
  channel.close();
}
