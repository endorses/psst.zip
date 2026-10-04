import { normalizeScanOrigin } from "./scan-input.ts";

export function validAbuseEmail(value: unknown): value is string {
  if (typeof value !== "string" || value.length > 254) return false;
  const parts = value.split("@");
  if (parts.length !== 2) return false;
  const [local, domain] = parts;
  return (
    local.length > 0 &&
    local.length <= 64 &&
    /^[A-Za-z0-9._+%-]+$/.test(local) &&
    !local.startsWith(".") &&
    !local.endsWith(".") &&
    !local.includes("..") &&
    domain.split(".").length >= 2 &&
    domain
      .split(".")
      .every((label) => /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(label))
  );
}

/** Accept only separately supplied origin/path, never a complete capability URL. */
export function abuseReference(origin: string, pathname = "/"): string {
  const normalized = normalizeScanOrigin(origin);
  if (!normalized) throw new Error("Invalid server address");
  const match =
    /^\/([du])\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\/?$/i.exec(pathname);
  return (
    `Instance: ${normalized}` +
    (match
      ? `\nResource type: ${match[1].toLowerCase() === "d" ? "transfer" : "slot"}\nResource ID: ${match[2].toLowerCase()}`
      : "")
  );
}

export function abuseMailto(email: string, origin: string, pathname = "/"): string {
  if (!validAbuseEmail(email)) throw new Error("Invalid abuse contact");
  const body = `${abuseReference(origin, pathname)}\n\nDescribe your concern here. Do not include the complete file link, encryption keys or file contents.`;
  return `mailto:${encodeURIComponent(email)}?subject=${encodeURIComponent("psst.zip abuse report")}&body=${encodeURIComponent(body)}`;
}

/** Same-origin only; no credentials, redirects, referrer or unbounded JSON. */
export async function loadAbuseContact(signal: AbortSignal): Promise<string> {
  const response = await fetch("/api/v1/config", {
    credentials: "omit",
    redirect: "error",
    cache: "no-store",
    referrerPolicy: "no-referrer",
    signal,
  });
  if (!response.ok || !response.body) throw new Error("Abuse contact unavailable");
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > 65536) throw new Error("Abuse contact response is too large");
      chunks.push(value);
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  const value: unknown = JSON.parse(new TextDecoder().decode(bytes));
  if (!value || typeof value !== "object") return "";
  const email = (value as { abuse_contact_email?: unknown }).abuse_contact_email;
  return validAbuseEmail(email) ? email : "";
}
