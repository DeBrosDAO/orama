// Turns the built single-page app into one real HTML file per page:
//   dist/index.html, dist/platform/index.html, dist/blog/<slug>/index.html, …
// Each file carries the fully rendered page plus its own <title>, description,
// canonical URL, robots directives, link-preview tags and schema.org JSON-LD,
// so search engines and chat apps see real content and the browser hydrates
// instead of rendering from nothing. Also writes dist/404.html, the sitemaps
// (an index at sitemap.xml and one per part of the site), the blog's RSS feed
// and robots.txt. The page list is PAGES in src/content/pages.ts.
//
// Runs after `vite build` (client, into dist/, with a manifest) and
// `vite build --ssr src/entry-server.tsx` (server, into dist-server/).

import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const SITE_ROOT = resolve(HERE, "..");
const DIST = resolve(HERE, "../dist");
const SERVER_DIR = resolve(HERE, "../dist-server");
const MANIFEST = join(DIST, ".vite", "manifest.json");
const PAGE_SOURCES = join(DIST, ".vite", "page-sources.json");

const ROOT_EMPTY = '<div id="root"></div>';
/** Text only the 404 page renders; seeing it on a listed page means the page is missing. */
const NOT_FOUND_TEXT = "This page doesn&#x27;t exist.";
/** React's out-of-order streaming markers: content hidden behind a fallback. */
const STREAMING_MARKERS = ["<!--$?-->", '<template id="B:'];
/** Builds run on the day they run; a file with uncommitted changes changed today. */
const TODAY = new Date().toISOString().slice(0, 10);

// Served one page's HTML for a path it wasn't rendered for (a 404, a deep
// explorer link), clear it before first paint instead of flashing the wrong
// page; the app then renders the right page from scratch. It inlines
// normalizePath from src/content/routes.ts, the same function src/main.tsx uses.
const mismatchGuard = (normalizePath) =>
  `<script>(function(){var n=${normalizePath.toString()};` +
  'var r=document.getElementById("root");' +
  'if(r.getAttribute("data-prerendered")!==n(location.pathname))r.replaceChildren()})()</script>';

const escapeAttr = (s) =>
  s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

/**
 * Replace exactly one match, or fail the build: a silent miss ships a wrong
 * head. The replacement is passed as a function so "$&", "$$" and friends in
 * page text are inserted literally, not interpreted as patterns.
 */
function replaceOnce(html, pattern, replacement, what) {
  const count = html.split(pattern).length - 1;
  if (count !== 1) {
    throw new Error(`prerender: expected exactly one ${what} in index.html, found ${count}`);
  }
  return html.replace(pattern, () => replacement);
}

function metaReplace(html, attr, name, value) {
  const tag = `<meta ${attr}="${name}" content="`;
  const start = html.indexOf(tag);
  if (start === -1 || html.indexOf(tag, start + 1) !== -1) {
    throw new Error(`prerender: expected exactly one ${attr}="${name}" in index.html`);
  }
  const end = html.indexOf('" />', start);
  if (end === -1) throw new Error(`prerender: unterminated ${attr}="${name}" tag in index.html`);
  return html.slice(0, start) + tag + escapeAttr(value) + html.slice(end);
}

/** The page's own JS chunks and everything they import, for <link rel=modulepreload>. */
function preloadsFor(manifest, page) {
  const files = new Set();
  const visit = (key) => {
    const entry = manifest[key];
    if (!entry || entry.isEntry || files.has(entry.file)) return;
    files.add(entry.file);
    for (const dep of entry.imports ?? []) visit(dep);
  };
  for (const chunk of page.chunks) {
    if (!manifest[chunk]) throw new Error(`prerender: no chunk for ${page.path} in the Vite manifest (expected key ${chunk})`);
    visit(chunk);
  }
  return [...files].map((f) => `<link rel="modulepreload" crossorigin href="/${f}" />`).join("\n    ");
}

/** Each page's source files, less those every page shares (layout, head, page list): those are the site's frame, not the page's content. */
function contentSources(pageSources, pages) {
  const perPage = pages.map((page) => {
    const files = new Set();
    for (const chunk of page.chunks) {
      const sources = pageSources[chunk];
      if (!sources) throw new Error(`prerender: no source list for ${chunk} (${page.path}) in page-sources.json`);
      for (const f of sources) files.add(f);
    }
    return files;
  });
  const shared = [...perPage[0]].filter((f) => perPage.every((files) => files.has(f)));
  return new Map(pages.map((page, i) => [page.path, [...perPage[i]].filter((f) => !shared.includes(f))]));
}

/** The newest commit date of any of files; today if one has uncommitted changes. */
function lastmodFromGit(path, files) {
  if (files.length === 0) throw new Error(`prerender: ${path} has no sources of its own to date it by`);
  const git = (...args) => execFileSync("git", [...args, "--", ...files], { cwd: SITE_ROOT, encoding: "utf-8" }).trim();
  if (git("status", "--porcelain")) return TODAY;
  const date = git("log", "-1", "--format=%cs");
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) throw new Error(`prerender: no git date for the sources of ${path} ("${date}")`);
  return date;
}

function headFor(html, server, page, { noindex }) {
  const url = server.absoluteUrl(page.path);
  const image = server.absoluteUrl(page.image.path);
  html = replaceOnce(html, /<title>[^<]*<\/title>/, `<title>${escapeAttr(page.title)}</title>`, "<title>");
  html = metaReplace(html, "name", "description", page.description);
  html = metaReplace(html, "name", "robots", noindex ? server.ROBOTS_NOINDEX : server.ROBOTS_INDEX);
  html = metaReplace(html, "property", "og:type", page.post ? "article" : "website");
  html = metaReplace(html, "property", "og:title", page.title);
  html = metaReplace(html, "property", "og:description", page.description);
  html = metaReplace(html, "property", "og:url", url);
  html = metaReplace(html, "property", "og:image", image);
  html = metaReplace(html, "property", "og:image:alt", page.image.alt);
  html = metaReplace(html, "name", "twitter:title", page.title);
  html = metaReplace(html, "name", "twitter:description", page.description);
  html = metaReplace(html, "name", "twitter:image", image);
  html = metaReplace(html, "name", "twitter:image:alt", page.image.alt);
  return replaceOnce(html, /<link rel="canonical" href="[^"]*" \/>/, `<link rel="canonical" href="${url}" />`, "canonical link");
}

function pageHtml(template, server, page, { body, lastmod, preloads, guard, noindex }) {
  let html = headFor(template, server, page, { noindex: Boolean(noindex) });
  const extra = [preloads, server.extraHeadTags(page)].filter(Boolean).join("\n    ");
  html = replaceOnce(
    html,
    "</head>",
    `    ${extra}\n    <script type="application/ld+json">${server.structuredData(page, lastmod)}</script>\n  </head>`,
    "</head>",
  );
  return replaceOnce(html, ROOT_EMPTY, `<div id="root" data-prerendered="${page.path}">${body}</div>${guard}`, "empty #root");
}

function checkBody(path, body, { expectNotFound = false } = {}) {
  if (!body.trim()) throw new Error(`prerender: ${path} rendered empty`);
  const h1s = body.match(/<h1[\s>]/g) ?? [];
  if (h1s.length !== 1) throw new Error(`prerender: ${path} has ${h1s.length} <h1> elements; a page needs exactly one`);
  if (!expectNotFound && body.includes(NOT_FOUND_TEXT)) {
    throw new Error(`prerender: ${path} rendered the 404 page; is it missing from src/app.tsx?`);
  }
  for (const marker of STREAMING_MARKERS) {
    if (body.includes(marker)) {
      throw new Error(`prerender: ${path} contains streamed-in content (${marker}); crawlers would see a spinner`);
    }
  }
}

function writePage(path, html) {
  const out = path === "/" ? join(DIST, "index.html") : join(DIST, path, "index.html");
  mkdirSync(dirname(out), { recursive: true });
  writeFileSync(out, html);
}

/** dist/404.html: what nginx answers, with a 404 status, for any address no page owns. */
async function writeNotFound(server, template, guard) {
  const body = await server.render(server.NOT_FOUND_PROBE);
  checkBody("404", body, { expectNotFound: true });
  if (!body.includes(NOT_FOUND_TEXT)) throw new Error("prerender: the 404 probe did not render the 404 page");
  const page = {
    path: server.NOT_FOUND_PROBE,
    title: "Page not found · Orama Network",
    description: "This page doesn't exist. Find Orama Network's platform, docs and blog from the home page.",
    image: { path: "/og-image.png", alt: "Orama Network" },
    crumbs: [],
    schema: "WebPage",
  };
  let html = headFor(template, server, page, { noindex: true });
  html = replaceOnce(html, /\s*<link rel="canonical" href="[^"]*" \/>/, "", "canonical link");
  // No data-prerendered: the guard always clears it and the app renders the 404 for the real address.
  html = replaceOnce(html, ROOT_EMPTY, `<div id="root">${body}</div>${guard}`, "empty #root");
  writeFileSync(join(DIST, "404.html"), html);
}

function writeSitemaps(server, lastmods) {
  const index = [];
  for (const group of server.SITEMAP_GROUPS) {
    const pages = server.PAGES.filter((p) => p.sitemap === group);
    if (pages.length === 0) continue;
    writeFileSync(join(DIST, server.sitemapPath(group).slice(1)), server.sitemapXml(pages, (p) => lastmods.get(p.path)));
    index.push({ group, lastmod: pages.map((p) => lastmods.get(p.path)).sort().at(-1) });
  }
  writeFileSync(join(DIST, "sitemap.xml"), server.sitemapIndexXml(index));
  return index.length;
}

async function main() {
  for (const f of [MANIFEST, PAGE_SOURCES]) {
    if (!existsSync(f)) throw new Error(`prerender: ${f} missing; run vite build first (build.manifest and the page-sources plugin write it)`);
  }
  const manifest = JSON.parse(readFileSync(MANIFEST, "utf-8"));
  const pageSources = JSON.parse(readFileSync(PAGE_SOURCES, "utf-8"));
  const server = await import(pathToFileURL(join(SERVER_DIR, "entry-server.js")).href);
  const template = readFileSync(join(DIST, "index.html"), "utf-8");
  const guard = mismatchGuard(server.normalizePath);

  const sources = contentSources(pageSources, server.PAGES);
  const lastmods = new Map();
  for (const page of server.PAGES) {
    const body = await server.render(page.path);
    checkBody(page.path, body);
    const lastmod = server.knownLastModified(page) ?? lastmodFromGit(page.path, sources.get(page.path));
    lastmods.set(page.path, lastmod);
    const preloads = preloadsFor(manifest, page);
    writePage(page.path, pageHtml(template, server, page, { body, lastmod, preloads, guard, noindex: page.noindex }));
  }
  await writeNotFound(server, template, guard);

  const sitemaps = writeSitemaps(server, lastmods);
  mkdirSync(join(DIST, dirname(server.BLOG_FEED_PATH)), { recursive: true });
  writeFileSync(join(DIST, server.BLOG_FEED_PATH.slice(1)), server.rssFeed(server.POSTS));
  writeFileSync(join(DIST, "robots.txt"), `User-agent: *\nAllow: /\n\nSitemap: ${server.SITE_URL}/sitemap.xml\n`);

  // The manifest is a build-only artefact, not the site. dist-server/ stays
  // for build-og.mjs and build-pdf.mjs; it sits outside dist/, so it is never published.
  rmSync(join(DIST, ".vite"), { recursive: true, force: true });
  console.log(
    `prerender: ${server.PAGES.length} pages + 404.html, sitemap index + ${sitemaps} sitemaps, ` +
      `${server.BLOG_FEED_PATH} (${server.POSTS.length} posts), robots.txt`,
  );
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
