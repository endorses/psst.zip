export function validateLinkLimit(value: number): number {
  if (!Number.isInteger(value) || value < 0 || value > 2_147_483_647)
    throw new Error("Choose a whole-number limit between 1 and 2147483647, or turn the limit off.");
  return value;
}
