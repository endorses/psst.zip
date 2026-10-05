/** Section identifiers stay independent of their translated labels. */
export function selectedAdminSection(
  url: URL,
  allowed: readonly string[],
  fallback: string,
): string {
  const section = url.searchParams.get("section");
  return section && allowed.includes(section) ? section : fallback;
}

export function adminSectionUrl(view: "server" | "traffic", section: string): string {
  return `/?${new URLSearchParams({ view, section })}`;
}
