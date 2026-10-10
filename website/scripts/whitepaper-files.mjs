// Which whitepaper PDFs the site serves, where they come from in the repo, and
// what the page shows about each one. Pure functions: scripts/build-whitepaper.mjs
// does the file work, and whitepaper-files.test.mjs covers this logic.

/** Public directory (under website/public) the PDFs are copied into, and its URL prefix. */
export const PUBLIC_DIR = "whitepaper";

/** The command that produces the PDFs, named in the error when one is missing. */
export const BUILD_HINT = "run `make whitepaper whitepaper-short` from the repo root";

const SHORT_DIST = "docs/whitepaper/orama-whitepaper/dist";
const REFERENCE_DIST = "docs/whitepaper/technical-reference/dist";

/**
 * The PDFs, in page order. `source` is repo-relative and carries the version
 * the files were built at; `stable` is the name that never changes, `versioned`
 * the name that never changes meaning.
 */
export function whitepaperPlan(version) {
  if (!/^\d+\.\d+\.\d+$/.test(version)) {
    throw new Error(`whitepaper: VERSION must look like 1.2.3, got "${version}"`);
  }
  const ref = (part, title) => ({
    key: part,
    edition: "reference",
    title,
    source: `${REFERENCE_DIST}/orama-whitepaper-technical-reference-v${version}-${part}.pdf`,
    stable: `orama-whitepaper-technical-reference-${part}.pdf`,
    versioned: `orama-whitepaper-technical-reference-v${version}-${part}.pdf`,
  });
  return [
    {
      key: "short",
      edition: "short",
      title: "Orama Whitepaper: Technical Edition",
      source: `${SHORT_DIST}/orama-whitepaper-v${version}.pdf`,
      stable: "orama-whitepaper.pdf",
      versioned: `orama-whitepaper-v${version}.pdf`,
    },
    ref("vol1", "Volume I: The Platform"),
    ref("vol2", "Volume II: The Global Layer"),
    ref("appendices", "Appendices"),
  ];
}

/** "1.6 MB", "812 KB": decimal units, one decimal from a megabyte up. */
export function formatBytes(bytes) {
  if (!Number.isInteger(bytes) || bytes < 0) throw new Error(`whitepaper: invalid size ${bytes}`);
  if (bytes >= 1_000_000) return `${(bytes / 1_000_000).toFixed(1)} MB`;
  return `${Math.max(1, Math.round(bytes / 1000))} KB`;
}

/** The page count in `pdfinfo` output. */
export function parsePageCount(pdfinfoOutput) {
  const match = /^Pages:\s+(\d+)\s*$/m.exec(pdfinfoOutput);
  if (!match || Number(match[1]) < 1) throw new Error("whitepaper: pdfinfo printed no page count");
  return Number(match[1]);
}

/** The first bytes of a PDF file. */
export function isPdf(bytes) {
  return bytes.subarray(0, 5).toString("latin1") === "%PDF-";
}

/**
 * The generated manifest the page reads: the version, each file's URL path,
 * size and page count, and the reference's totals.
 * `measured` maps a plan key to { bytes, pages }.
 */
export function buildManifest(version, plan, measured) {
  const files = plan.map((entry) => {
    const m = measured[entry.key];
    if (!m) throw new Error(`whitepaper: no measurement for ${entry.key}`);
    return {
      key: entry.key,
      edition: entry.edition,
      title: entry.title,
      href: `/${PUBLIC_DIR}/${entry.versioned}`,
      stableHref: `/${PUBLIC_DIR}/${entry.stable}`,
      bytes: m.bytes,
      size: formatBytes(m.bytes),
      pages: m.pages,
    };
  });
  const reference = files.filter((f) => f.edition === "reference");
  const referenceBytes = reference.reduce((sum, f) => sum + f.bytes, 0);
  return {
    version,
    files,
    referenceTotal: {
      bytes: referenceBytes,
      size: formatBytes(referenceBytes),
      pages: reference.reduce((sum, f) => sum + f.pages, 0),
    },
  };
}
