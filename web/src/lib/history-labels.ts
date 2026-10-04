export type HistoryLabel = { title?: string; custom?: string; size?: number };
export type HistoryLabels = Record<string, HistoryLabel>;
export function labelKey(kind: "transfers" | "slots", id: string) {
  return `${kind}:${id}`;
}
export function loadLabels(account: string): HistoryLabels {
  try {
    return JSON.parse(localStorage.getItem(`psst.labels.${account}`) || "{}");
  } catch {
    return {};
  }
}
export function saveLabels(account: string, labels: HistoryLabels) {
  try {
    localStorage.setItem(`psst.labels.${account}`, JSON.stringify(labels));
  } catch {
    /* Local-only labels remain usable for this session. */
  }
}
export function labelFor(
  labels: HistoryLabels,
  kind: "transfers" | "slots",
  id: string,
): HistoryLabel {
  return labels[labelKey(kind, id)] ?? labels[id] ?? {};
}
// Keep extensions visible while the full name remains available in the title/accessible name.
export function compactTitle(value: string, max = 72): string {
  if (Array.from(value).length <= max) return value;
  const suffix = value.match(/(\.[^ .]{1,12})( \+ \d+ files?)?$/u)?.[0] ?? "";
  return (
    Array.from(value)
      .slice(0, max - suffix.length - 1)
      .join("") +
    "…" +
    suffix
  );
}
