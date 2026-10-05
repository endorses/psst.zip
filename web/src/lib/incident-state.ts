import { message as m, LocalizedError, type DisplayText } from "./i18n/index.ts";
export interface IncidentState {
  public_transfers_paused: boolean;
  updated_at: string;
}

export class TransferStateError extends LocalizedError {
  code: "public_transfers_paused" | "resource_revoked";
  constructor(code: "public_transfers_paused" | "resource_revoked", message: DisplayText) {
    super(message);
    this.code = code;
  }
}

export function transferStateError(code: unknown): TransferStateError | null {
  if (code === "public_transfers_paused")
    return new TransferStateError(code, m("publicTransfersArePausedByThisServerSAdministrator"));
  if (code === "resource_revoked")
    return new TransferStateError(code, m("thisTransferWasRevokedAskTheSenderForA"));
  return null;
}

/** A terminated stream cannot carry a JSON error; inspect public control metadata only. */
export async function detectPublicPause(signal?: AbortSignal): Promise<TransferStateError | null> {
  if (signal?.aborted) return null;
  try {
    const response = await fetch("/api/v1/config", {
      credentials: "omit",
      cache: "no-store",
      signal: AbortSignal.timeout(2500),
    });
    if (!response.ok || signal?.aborted) return null;
    const config = await response.json();
    return config.public_transfers_paused === true
      ? transferStateError("public_transfers_paused")
      : null;
  } catch {
    return null;
  }
}

export function validateIncidentState(value: unknown): IncidentState {
  const state = value as IncidentState;
  if (
    !state ||
    typeof state.public_transfers_paused !== "boolean" ||
    typeof state.updated_at !== "string"
  )
    throw new LocalizedError(m("theServerReturnedAnUnsupportedTransferPauseStateRefresh"));
  return state;
}
