import { derived, get, writable } from "svelte/store";
import {
  normalizePreference,
  regionalLocale,
  resolveLanguage,
  languageStorageKey,
  type LanguagePreference,
} from "./locale.ts";
import {
  renderMessage,
  message,
  isMessage,
  type DisplayText,
  type MessageArgument,
  type MessageKey,
} from "./messages.ts";
export {
  message,
  dateArgument,
  MessageError,
  LocalizedError,
  errorText,
  isMessage,
  type DisplayText,
  type Message,
  type MessageKey,
} from "./messages.ts";
const browserLanguages = () =>
  typeof navigator === "undefined" ? ["en"] : [...navigator.languages];
export const languageReady = writable(false);
export const preference = writable<LanguagePreference>(
  typeof document === "undefined"
    ? "system"
    : normalizePreference(document.documentElement.dataset.language),
);
const languages = writable(browserLanguages());
export const language = derived([preference, languages], ([value, list]) =>
  resolveLanguage(value, list),
);
export const locale = derived([language, languages], ([value, list]) =>
  regionalLocale(value, list),
);
export const t = derived(
  [language, locale],
  ([lang, regional]) =>
    (value: unknown) =>
      typeof value === "number"
        ? new Intl.NumberFormat(regional).format(value)
        : typeof value === "string" || isMessage(value)
          ? renderMessage(value, lang, regional)
          : value == null
            ? ""
            : String(value),
);
export function translate(value: DisplayText): string {
  return get(t)(value);
}
export function text(
  key: MessageKey,
  args: Readonly<Record<string, MessageArgument>> = {},
): string {
  return translate({ kind: "psst.message", key, args });
}
export function chooseLanguage(value: unknown) {
  const next = normalizePreference(value);
  preference.set(next);
  try {
    localStorage.setItem(languageStorageKey, next);
  } catch {
    /* In-memory preference remains usable. */
  }
}
export function initializeLanguage(): () => void {
  try {
    preference.set(normalizePreference(localStorage.getItem(languageStorageKey)));
  } catch {
    /* Respect the current in-memory/startup value. */
  }
  const update = () => languages.set(browserLanguages());
  update();
  languageReady.set(true);
  const unsubscribe = language.subscribe((value) => {
    document.documentElement.lang = value;
  });
  const sync = (event: StorageEvent) => {
    if (event.key === languageStorageKey || event.key === null)
      preference.set(normalizePreference(event.newValue));
  };
  window.addEventListener("storage", sync);
  window.addEventListener("languagechange", update);
  return () => {
    unsubscribe();
    window.removeEventListener("storage", sync);
    window.removeEventListener("languagechange", update);
  };
}
export function number(value: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(get(locale), options).format(value);
}
export function date(value: string | number | Date, options?: Intl.DateTimeFormatOptions): string {
  return new Intl.DateTimeFormat(
    get(locale),
    options ?? { dateStyle: "medium", timeStyle: "short" },
  ).format(new Date(value));
}

export function calendarDate(value: string): string {
  return date(value, { dateStyle: "medium", timeZone: "UTC" });
}
