const SUFFIX = "Orama Explorer";
const DEMO_MARK = " (demo)";

/** The browser tab title for a page; a demo source is marked so a tab never passes for the real chain. */
export function documentTitle(title: string, demo: boolean): string {
  return `${title} · ${SUFFIX}${demo ? DEMO_MARK : ""}`;
}
