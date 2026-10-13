import manifest from "../../.generated/whitepaper.json";
import { GITHUB_URL } from "./site";

/**
 * The whitepaper PDFs the page offers: version, size and page count of each,
 * measured from the built files by scripts/build-whitepaper.mjs.
 */
export interface WhitepaperFile {
  key: string;
  edition: "short" | "reference";
  title: string;
  /** Versioned URL: never changes meaning, cached for good. */
  href: string;
  /** Stable URL: always the current version, for links from elsewhere. */
  stableHref: string;
  size: string;
  pages: number;
}

export interface WhitepaperManifest {
  version: string;
  files: WhitepaperFile[];
  referenceTotal: { size: string; pages: number };
}

export const WHITEPAPER = manifest as WhitepaperManifest;

export const WHITEPAPER_SHORT = WHITEPAPER.files.filter((f) => f.edition === "short")[0];
export const WHITEPAPER_REFERENCE = WHITEPAPER.files.filter((f) => f.edition === "reference");

/** Where the chapter-by-chapter source of both editions lives in the repository. */
export const WHITEPAPER_SOURCE_PATH = "docs/whitepaper/";
export const WHITEPAPER_SOURCE_URL = `${GITHUB_URL}/tree/HEAD/${WHITEPAPER_SOURCE_PATH}`;
