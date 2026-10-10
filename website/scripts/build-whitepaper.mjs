// Copies the whitepaper PDFs from the repo's build outputs (docs/whitepaper/**/dist,
// gitignored) into public/whitepaper/ (gitignored) under a stable name and a
// versioned name, and writes .generated/whitepaper.json (version, size and page
// count of each file) for the whitepaper page.
//
// Fails, saying what to run, when a PDF is missing, is not a PDF, or its page
// count cannot be read: the page must never link to a file the site does not have.
// Page counts come from `pdfinfo` (poppler).
import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { BUILD_HINT, PUBLIC_DIR, buildManifest, isPdf, parsePageCount, whitepaperPlan } from "./whitepaper-files.mjs";

const SITE = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const REPO = resolve(SITE, "..");
const OUT_DIR = join(SITE, "public", PUBLIC_DIR);
const MANIFEST = join(SITE, ".generated", "whitepaper.json");

function pageCount(file) {
  try {
    return parsePageCount(execFileSync("pdfinfo", [file], { encoding: "utf-8" }));
  } catch (err) {
    throw new Error(`build-whitepaper: cannot read the page count of ${file} (install poppler for pdfinfo): ${err.message}`);
  }
}

function main() {
  const version = readFileSync(join(REPO, "VERSION"), "utf-8").trim();
  const plan = whitepaperPlan(version);

  const missing = plan.map((e) => join(REPO, e.source)).filter((f) => !existsSync(f));
  if (missing.length > 0) {
    throw new Error(
      `build-whitepaper: the whitepaper PDFs for v${version} are not built; ${BUILD_HINT}. Missing:\n  ${missing.join("\n  ")}`,
    );
  }

  rmSync(OUT_DIR, { recursive: true, force: true });
  mkdirSync(OUT_DIR, { recursive: true });
  const measured = {};
  for (const entry of plan) {
    const source = join(REPO, entry.source);
    const bytes = readFileSync(source);
    if (!isPdf(bytes)) throw new Error(`build-whitepaper: ${source} is not a PDF; ${BUILD_HINT}`);
    copyFileSync(source, join(OUT_DIR, entry.stable));
    copyFileSync(source, join(OUT_DIR, entry.versioned));
    measured[entry.key] = { bytes: bytes.length, pages: pageCount(source) };
  }

  mkdirSync(dirname(MANIFEST), { recursive: true });
  writeFileSync(MANIFEST, `${JSON.stringify(buildManifest(version, plan, measured), null, 2)}\n`);
  console.log(`build-whitepaper: ${plan.length} PDFs (v${version}) -> public/${PUBLIC_DIR}/`);
}

main();
