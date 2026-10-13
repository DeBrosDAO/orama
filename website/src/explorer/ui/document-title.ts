const SUFFIX = "Orama Explorer";

/** The browser tab title for a page. */
export function documentTitle(title: string): string {
  return `${title} · ${SUFFIX}`;
}
