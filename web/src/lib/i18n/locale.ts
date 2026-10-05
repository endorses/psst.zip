export type Language = "en" | "de";
export type LanguagePreference = "system" | Language;
export const languageStorageKey = "psst.language";
export function normalizePreference(value: unknown): LanguagePreference {
  return value === "en" || value === "de" ? value : "system";
}
export function resolveLanguage(
  preference: LanguagePreference,
  languages: readonly string[],
): Language {
  if (preference !== "system") return preference;
  for (const value of languages) {
    const base = value.toLowerCase().split(/[-_]/)[0];
    if (base === "en" || base === "de") return base;
  }
  return "en";
}
export function regionalLocale(language: Language, languages: readonly string[]): string {
  for (const value of languages) {
    try {
      const locale = new Intl.Locale(value.replaceAll("_", "-"));
      if (locale.language === language) return locale.toString();
    } catch {
      /* Ignore invalid browser preferences. */
    }
  }
  return language === "de" ? "de-DE" : "en-US";
}
