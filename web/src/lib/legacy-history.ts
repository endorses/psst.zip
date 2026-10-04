/** Legacy localStorage requires one whole-string allocation. Parse only one bounded
 * entry at a time; callers yield and commit batches instead of building a full map. */
export const LEGACY_ENTRY_CHARS = 64 * 1024;
export function* legacyHistoryEntries(source: string): Generator<[string, unknown]> {
  let index = 0;
  const whitespace = () => {
    const start = index;
    while (/[ \t\r\n]/.test(source[index] ?? "x")) {
      if (index - start > LEGACY_ENTRY_CHARS) throw fail();
      index++;
    }
  };
  const fail = () =>
    new Error(
      "Existing local history could not be imported. Its original data is unchanged. Retry after restoring a valid browser backup.",
    );
  whitespace();
  if (source[index++] !== "{") throw fail();
  whitespace();
  if (source[index] === "}") {
    index++;
    whitespace();
    if (index !== source.length) throw fail();
    return;
  }
  while (index < source.length) {
    whitespace();
    const start = index;
    if (source[index++] !== '"') throw fail();
    while (index < source.length) {
      if (index - start > 2048) throw fail();
      if (source[index] === "\\") {
        index += 2;
        continue;
      }
      if (source[index++] === '"') break;
    }
    let key: unknown;
    try {
      key = JSON.parse(source.slice(start, index));
    } catch {
      throw fail();
    }
    if (typeof key !== "string" || !key || key.length > 256) throw fail();
    whitespace();
    if (source[index++] !== ":") throw fail();
    whitespace();
    const valueStart = index;
    let depth = 0,
      quoted = false;
    while (index < source.length) {
      if (index - valueStart > LEGACY_ENTRY_CHARS) throw fail();
      const char = source[index];
      if (quoted) {
        if (char === "\\") {
          index += 2;
          continue;
        }
        if (char === '"') quoted = false;
      } else if (char === '"') quoted = true;
      else if (char === "{" || char === "[") {
        if (++depth > 16) throw fail();
      } else if ((char === "}" || char === ",") && depth === 0) break;
      else if (char === "}" || char === "]") {
        if (--depth < 0) throw fail();
      }
      index++;
    }
    if (quoted || depth !== 0 || index === source.length) throw fail();
    let value: unknown;
    try {
      value = JSON.parse(source.slice(valueStart, index));
    } catch {
      throw fail();
    }
    yield [key, value];
    const delimiter = source[index++];
    whitespace();
    if (delimiter === "}") {
      if (index !== source.length) throw fail();
      return;
    }
    if (delimiter !== "," || source[index] === "}") throw fail();
  }
  throw fail();
}
