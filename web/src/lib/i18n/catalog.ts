import en from "./en.json" with { type: "json" };
import deSource from "./de.json" with { type: "json" };
export { en };
export const de: Record<keyof typeof en, string | { one: string; other: string }> = deSource;
