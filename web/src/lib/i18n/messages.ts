import type { MessageArguments } from "./arguments.ts";
import { en, de } from "./catalog.ts";
export type MessageKey = keyof typeof en;
export type MessageArgument = string | number | null | Message | DateArgument;
export interface DateArgument {
  readonly kind: "psst.date";
  readonly value: string | number;
  readonly utc: boolean;
}
export function dateArgument(value: string | number, utc = false): DateArgument {
  return { kind: "psst.date", value, utc };
}
export interface Message {
  readonly kind: "psst.message";
  readonly key: MessageKey;
  readonly args: Readonly<Record<string, MessageArgument>>;
}
export type DisplayText = string | Message;
export function message<K extends MessageKey>(
  key: K,
  ...parameters: MessageArguments[K] extends never ? [] : [args: MessageArguments[K]]
): Message {
  return {
    kind: "psst.message",
    key,
    args: (parameters[0] ?? {}) as Readonly<Record<string, MessageArgument>>,
  };
}
export function isMessage(value: unknown): value is Message {
  return (
    !!value &&
    typeof value === "object" &&
    "kind" in value &&
    value.kind === "psst.message" &&
    "key" in value &&
    typeof value.key === "string" &&
    Object.hasOwn(en, value.key)
  );
}
export function renderMessage(
  value: DisplayText,
  language: "en" | "de",
  locale: string = language,
): string {
  if (!isMessage(value)) return value;
  const entry: string | { one: string; other: string } =
    (language === "de" ? de[value.key] : en[value.key]) ?? en[value.key];
  const count = value.args.count ?? value.args.arg0;
  const text =
    typeof entry === "string"
      ? entry
      : entry[
          typeof count === "number" && new Intl.PluralRules(language).select(count) === "one"
            ? "one"
            : "other"
        ];
  return text.replace(/\{([a-zA-Z][a-zA-Z0-9_]*)\}/g, (_, name: string) => {
    const argument = value.args[name];
    if (typeof argument === "number") return new Intl.NumberFormat(locale).format(argument);
    if (argument && typeof argument === "object" && argument.kind === "psst.date")
      return (
        new Intl.DateTimeFormat(locale, {
          dateStyle: "medium",
          timeStyle: "short",
          ...(argument.utc ? { timeZone: "UTC" } : {}),
        }).format(new Date(argument.value)) + (argument.utc ? " UTC" : "")
      );
    return isMessage(argument)
      ? renderMessage(argument, language, locale)
      : typeof argument === "string"
        ? argument
        : "";
  });
}
export class LocalizedError extends Error {
  readonly presentation: DisplayText;
  constructor(presentation: DisplayText) {
    super(renderMessage(presentation, "en"));
    this.presentation = presentation;
  }
}
export function errorText(
  error: unknown,
  fallback: DisplayText = message("unexpectedError"),
): DisplayText {
  return error instanceof LocalizedError ? error.presentation : fallback;
}
export class MessageError extends Error {
  readonly presentation: Message;
  constructor(key: MessageKey, args: Readonly<Record<string, MessageArgument>> = {}) {
    const presentation: Message = { kind: "psst.message", key, args };
    super(renderMessage(presentation, "en"));
    this.presentation = presentation;
  }
}
