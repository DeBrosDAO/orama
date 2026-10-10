import { APPS } from "./apps";
import { SERVICES } from "./services";

/**
 * The proof strip. Every number is either derived from data on this site or
 * read from the repository at build time (vite.config.ts) — none are typed in
 * by hand, so none can drift.
 */
export interface Fact {
  value: string;
  label: string;
}

const since = new Date(`${__REPO_FIRST_COMMIT__}T00:00:00Z`).toLocaleDateString(
  "en-GB",
  { month: "short", year: "numeric", timeZone: "UTC" },
);

export const FACTS: Fact[] = [
  { value: String(SERVICES.length), label: "services live" },
  { value: String(APPS.length), label: "apps in the ecosystem" },
  { value: __REPO_COMMITS__.toLocaleString("en-US"), label: `commits since ${since}` },
  { value: "AGPL", label: "open source" },
];

export const LICENSE = "AGPL-3.0";
