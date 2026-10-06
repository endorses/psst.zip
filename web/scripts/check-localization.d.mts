export interface LocalizationInventoryEntry {
  file: string;
  value: string;
  kind: string;
}

export interface LocalizationCheckResult {
  failures: string[];
  inventory: LocalizationInventoryEntry[];
  catalogCount: number;
}

/** Validate a source directory without launching a process or writing files. */
export function checkLocalization(root: string): LocalizationCheckResult;
