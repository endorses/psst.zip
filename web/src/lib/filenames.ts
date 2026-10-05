import { message as m, LocalizedError } from "./i18n/index.ts";
const encoder = new TextEncoder();
const MAX_NAME_BYTES = 200;

function parts(name: string): [string, string] {
  const extension = /\.[\p{L}\p{N}]{1,16}$/u.exec(name)?.[0] ?? "";
  return extension ? [name.slice(0, -extension.length), extension] : [name, ""];
}

function fit(name: string, suffix = ""): string {
  const [stem, extension] = parts(name);
  const points = Array.from(stem);
  while (encoder.encode(points.join("") + suffix + extension).length > MAX_NAME_BYTES) points.pop();
  return points.join("") + suffix + extension;
}

/** One inert filesystem component, used for both display and saving. */
export function safeFilename(name: string): string {
  if (!name.trim() || name.length > 1024 || /[\\/\0]/.test(name))
    throw new LocalizedError(m("aFileHasAnUnsafeName"));
  let safe = name
    .normalize("NFC")
    .replace(/[\x00-\x1f\x7f-\x9f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069:*?"<>|]/g, "_")
    .trim()
    .replace(/[. ]+$/, "");
  if (!safe) throw new LocalizedError(m("aFileHasAnUnsafeName"));
  // Device names are reserved on Windows even with an extension.
  if (/^(?:con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])(?:\.|$)/i.test(safe)) safe = "_" + safe;
  return fit(safe);
}

/** Sanitization and case-insensitive filesystems must not merge distinct files. */
export function uniqueFilenames(names: string[]): string[] {
  const used = new Set<string>();
  return names.map((name) => {
    const base = safeFilename(name);
    let candidate = base;
    for (let counter = 2; used.has(candidate.toLowerCase()); counter++)
      candidate = fit(base, ` (${counter})`);
    used.add(candidate.toLowerCase());
    return candidate;
  });
}

/** An index prefix keeps ZIP entries distinct, including special object keys. */
export function zipEntryName(name: string, index: number): string {
  if (!Number.isSafeInteger(index) || index < 0) throw new LocalizedError(m("invalidFileIndex"));
  return safeFilename(`${index + 1}-${safeFilename(name)}`);
}
