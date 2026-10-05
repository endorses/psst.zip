import { message as m, LocalizedError } from "./i18n/index.ts";
export function validateLinkLimit(value: number): number {
  if (!Number.isInteger(value) || value < 0 || value > 2_147_483_647)
    throw new LocalizedError(m("chooseAWholeNumberLimitBetweenAndOrTurn"));
  return value;
}
