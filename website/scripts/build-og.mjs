// Draws the 1200x630 link-preview image of every page that has its own
// (each blog post, each doc, the blog): the page's title on the site's dark
// card, written to dist/<image path>. Chat apps and social networks show it
// when the page is shared; Google Discover prefers large page-specific images.
//
// Needs Google Chrome or Chromium, like build-pdf.mjs: set CHROME_PATH, or it
// is looked for in the usual install locations. Nothing is fetched from the
// network: fonts and the logo are inlined.
//
// Runs after prerender.mjs (reads dist-server/).

import { mkdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import puppeteer from "puppeteer-core";
import { findChrome } from "./chrome.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(HERE, "..");
const DIST = join(ROOT, "dist");
const SERVER_ENTRY = join(ROOT, "dist-server/entry-server.js");
const FONTS = join(ROOT, "node_modules/@fontsource-variable");
/** A real card is tens of KB; less means it rendered blank. */
const MIN_PNG_BYTES = 8_000;

const dataUrl = (file, type) => `data:${type};base64,${readFileSync(file).toString("base64")}`;

const escapeHtml = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

/** Long titles step down in size so every title fits in three lines. */
function titleSize(title) {
  if (title.length <= 30) return 92;
  if (title.length <= 55) return 76;
  if (title.length <= 80) return 62;
  return 52;
}

function cardHtml({ eyebrow, title, host, fonts, logo }) {
  return `<!doctype html><html><head><meta charset="utf-8"><style>
@font-face{font-family:"Display";src:url(${fonts.display}) format("woff2");font-weight:100 900}
@font-face{font-family:"Mono";src:url(${fonts.mono}) format("woff2");font-weight:100 900}
*{margin:0;box-sizing:border-box}
html,body{width:1200px;height:630px;background:#000}
body{position:relative;overflow:hidden;color:#e4e4e7;font-family:"Display",sans-serif;padding:72px 80px;display:flex;flex-direction:column}
.dots{position:absolute;inset:0;background-image:radial-gradient(#27272a 1.4px,transparent 1.4px);background-size:28px 28px;opacity:.9}
.glow{position:absolute;right:-220px;top:-260px;width:760px;height:760px;border-radius:50%;background:radial-gradient(closest-side,rgba(161,161,170,.18),transparent)}
.frame{position:absolute;inset:28px;border:1.5px dashed #3f3f46}
.top,.title,.bottom{position:relative}
.top{display:flex;align-items:center;gap:18px}
.top img{width:56px;height:56px}
.brand{font-weight:700;letter-spacing:.22em;font-size:26px}
.eyebrow{margin-top:auto;font-family:"Mono",monospace;font-size:24px;letter-spacing:.2em;text-transform:uppercase;color:#a1a1aa;position:relative}
.title{margin-top:20px;font-weight:700;letter-spacing:-.02em;line-height:1.05;max-width:1000px;display:-webkit-box;-webkit-line-clamp:3;-webkit-box-orient:vertical;overflow:hidden}
.bottom{margin-top:40px;font-family:"Mono",monospace;font-size:22px;color:#85858e;letter-spacing:.06em}
</style></head><body>
<div class="dots"></div><div class="glow"></div><div class="frame"></div>
<div class="top"><img src="${logo}" alt=""><span class="brand">ORAMA</span></div>
<div class="eyebrow">${escapeHtml(eyebrow)}</div>
<div class="title" style="font-size:${titleSize(title)}px">${escapeHtml(title)}</div>
<div class="bottom">${escapeHtml(host)}</div>
</body></html>`;
}

async function drawCard(browser, html, outFile) {
  const page = await browser.newPage();
  const problems = [];
  page.on("pageerror", (err) => problems.push(err.message));
  page.on("requestfailed", (req) => problems.push(`request failed: ${req.url().slice(0, 80)}`));
  try {
    await page.setViewport({ width: 1200, height: 630, deviceScaleFactor: 1 });
    await page.setContent(html, { waitUntil: "load" });
    await page.evaluate(() => document.fonts.ready);
    if (problems.length > 0) throw new Error(`build-og: ${outFile} did not render cleanly:\n  ${problems.join("\n  ")}`);
    mkdirSync(dirname(outFile), { recursive: true });
    await page.screenshot({ path: outFile, type: "png" });
  } finally {
    await page.close();
  }
  const size = statSync(outFile).size;
  if (size < MIN_PNG_BYTES) throw new Error(`build-og: ${outFile} is only ${size} bytes; the card likely rendered blank`);
}

async function main() {
  const server = await import(pathToFileURL(SERVER_ENTRY).href);
  const cards = server.PAGES.filter((p) => p.card);
  const shared = {
    host: new URL(server.SITE_URL).host,
    logo: dataUrl(join(ROOT, "src/assets/orama-icon.png"), "image/png"),
    fonts: {
      display: dataUrl(join(FONTS, "inter-tight/files/inter-tight-latin-wght-normal.woff2"), "font/woff2"),
      mono: dataUrl(join(FONTS, "jetbrains-mono/files/jetbrains-mono-latin-wght-normal.woff2"), "font/woff2"),
    },
  };
  const browser = await puppeteer.launch({ executablePath: findChrome("build-og"), headless: true });
  try {
    for (const page of cards) {
      await drawCard(browser, cardHtml({ ...shared, ...page.card }), join(DIST, page.image.path));
    }
  } finally {
    await browser.close();
  }
  console.log(`build-og: ${cards.length} preview images`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
