// Prints the built investor page to a PDF next to it (INVESTOR_PDF in
// src/content/site.ts), so investors can save or forward the page. The page's
// print stylesheet (src/index.css) lays it out for paper.
//
// Needs Google Chrome or Chromium: set CHROME_PATH, or it is looked for in the
// usual install locations. The page is loaded under the real site origin with
// every request answered from dist/, so links inside the PDF point at the live
// site and nothing is fetched from the network.
//
// Runs after prerender.mjs (reads dist/ and dist-server/).

import { existsSync, readFileSync } from "node:fs";
import { dirname, extname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import puppeteer from "puppeteer-core";
import { distFileFor } from "./dist-file.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const DIST = resolve(HERE, "../dist");
const SERVER_ENTRY = resolve(HERE, "../dist-server/entry-server.js");

const CHROME_CANDIDATES = [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/Applications/Chromium.app/Contents/MacOS/Chromium",
  "/usr/bin/google-chrome",
  "/usr/bin/google-chrome-stable",
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
];

const PAPER = "A4";
const MARGIN = { top: "12mm", bottom: "14mm", left: "12mm", right: "12mm" };
/** A4 less margins is ~700 CSS px; at this scale the page lays out at its ~1130 px desktop width. */
const PRINT_SCALE = 0.62;
const VIEWPORT = { width: 1280, height: 900 };
/** Recorded as the PDF's creator, in place of the headless browser's user agent. */
const PDF_CREATOR = "Orama Network";
/** A real printout of the page is several hundred KB; less means it came out empty. */
const MIN_PDF_BYTES = 50_000;

const CONTENT_TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript",
  ".css": "text/css",
  ".json": "application/json",
  ".woff2": "font/woff2",
  ".woff": "font/woff",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".webp": "image/webp",
  ".ico": "image/x-icon",
  ".txt": "text/plain; charset=utf-8",
  ".xml": "application/xml",
};

function findChrome() {
  const fromEnv = process.env.CHROME_PATH;
  if (fromEnv) {
    if (!existsSync(fromEnv)) throw new Error(`build-pdf: CHROME_PATH=${fromEnv} does not exist`);
    return fromEnv;
  }
  const found = CHROME_CANDIDATES.find((p) => existsSync(p));
  if (!found) {
    throw new Error(
      `build-pdf: no Chrome or Chromium found (looked in ${CHROME_CANDIDATES.join(", ")}). ` +
        "Install one, or set CHROME_PATH to its executable.",
    );
  }
  return found;
}

/** Answers the page's requests from dist/; anything else is recorded as a problem. */
function serveFromDist(page, siteOrigin, problems) {
  page.on("request", (req) => {
    const url = new URL(req.url());
    if (url.protocol === "data:") return req.continue();
    const file = url.origin === siteOrigin ? distFileFor(DIST, url.pathname) : null;
    if (!file) {
      problems.push(`unservable request: ${req.url()}`);
      return req.respond({ status: 404, body: "" });
    }
    const type = CONTENT_TYPES[extname(file)];
    if (!type) problems.push(`no content type for ${file}`);
    return req.respond({ status: 200, contentType: type, body: readFileSync(file) });
  });
  page.on("pageerror", (err) => problems.push(`page error: ${err.message}`));
  page.on("console", (msg) => {
    if (msg.type() === "error") problems.push(`console error: ${msg.text()}`);
  });
}

function footerTemplate(pageUrl) {
  const label = pageUrl.replace(/^https:\/\//, "").replace(/&/g, "&amp;").replace(/</g, "&lt;");
  return (
    '<div style="width:100%;padding:0 12mm;display:flex;justify-content:space-between;' +
    'font-family:system-ui,sans-serif;font-size:7px;color:#71717a">' +
    `<span>${label}</span>` +
    '<span><span class="pageNumber"></span> / <span class="totalPages"></span></span></div>'
  );
}

function failOnProblems(pageUrl, problems) {
  if (problems.length > 0) {
    throw new Error(`build-pdf: ${pageUrl} did not load cleanly:\n  ${problems.join("\n  ")}`);
  }
}

async function printPage(browser, siteOrigin, pageUrl, outFile) {
  const page = await browser.newPage();
  const problems = [];
  await page.setViewport(VIEWPORT);
  await page.setRequestInterception(true);
  serveFromDist(page, siteOrigin, problems);

  await page.goto(pageUrl, { waitUntil: "networkidle0" });
  await page.evaluate(async () => {
    for (const d of document.querySelectorAll("details")) d.open = true;
    await document.fonts.ready;
  });
  failOnProblems(pageUrl, problems);

  await page.pdf({
    path: outFile,
    format: PAPER,
    scale: PRINT_SCALE,
    margin: MARGIN,
    printBackground: true,
    outline: true,
    displayHeaderFooter: true,
    headerTemplate: "<span></span>",
    footerTemplate: footerTemplate(pageUrl),
  });
  // Printing can still request late resources (lazy images).
  failOnProblems(pageUrl, problems);
}

function checkPdf(outFile) {
  const bytes = readFileSync(outFile);
  if (bytes.subarray(0, 5).toString("latin1") !== "%PDF-") {
    throw new Error(`build-pdf: ${outFile} is not a PDF`);
  }
  if (bytes.length < MIN_PDF_BYTES) {
    throw new Error(`build-pdf: ${outFile} is only ${bytes.length} bytes; the page likely printed empty`);
  }
  return bytes.length;
}

async function main() {
  const { INVESTOR_PDF, SITE_URL } = await import(pathToFileURL(SERVER_ENTRY).href);
  const siteOrigin = new URL(SITE_URL).origin;
  const pageUrl = `${siteOrigin}${INVESTOR_PDF.page}`;
  const outFile = resolve(DIST, `.${INVESTOR_PDF.path}`);

  const browser = await puppeteer.launch({
    executablePath: findChrome(),
    headless: true,
    args: [`--user-agent=${PDF_CREATOR}`],
  });
  try {
    await printPage(browser, siteOrigin, pageUrl, outFile);
  } finally {
    await browser.close();
  }
  const size = checkPdf(outFile);
  console.log(`build-pdf: ${INVESTOR_PDF.page} -> dist${INVESTOR_PDF.path} (${Math.round(size / 1024)} KB)`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
