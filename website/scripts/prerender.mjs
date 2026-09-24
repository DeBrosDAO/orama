// Turns the built single-page app into one real HTML file per public page:
//   dist/index.html, dist/platform/index.html, …
// Each file carries the fully rendered page plus its own <title>, description,
// canonical URL, link-preview tags and schema.org JSON-LD, so search engines
// and chat apps see real content and the browser hydrates instead of
// rendering from nothing. Also writes sitemap.xml and robots.txt. The
// unlisted docs are left to the client.
//
// Runs after `vite build` (client, into dist/, with a manifest) and
// `vite build --ssr src/entry-server.tsx` (server, into dist-server/).

import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const DIST = resolve(HERE, "../dist");
const SERVER_DIR = resolve(HERE, "../dist-server");
const MANIFEST = join(DIST, ".vite", "manifest.json");

const ROOT_EMPTY = '<div id="root"></div>';
/** Text only the 404 page renders; seeing it means a route has no page. */
const NOT_FOUND_TEXT = "This page doesn&#x27;t exist.";
/** React's out-of-order streaming markers: content hidden behind a fallback. */
const STREAMING_MARKERS = ["<!--$?-->", '<template id="B:'];

// Served the home page's HTML for a path it wasn't rendered for (the docs, a
// 404), clear it before first paint instead of flashing the home page; the
// app then renders the right page from scratch. It inlines normalizePath
// from src/content/routes.ts, the same function src/main.tsx uses.
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

/** The page's own JS chunk and everything it imports, for <link rel=modulepreload>. */
function preloadsFor(manifest, route) {
  const src = `src/pages/${route.path === "/" ? "home" : route.path.slice(1)}.tsx`;
  if (!manifest[src]) {
    throw new Error(`prerender: no chunk for ${route.path} in the Vite manifest (expected key ${src})`);
  }
  const files = new Set();
  const visit = (key) => {
    const entry = manifest[key];
    if (!entry || entry.isEntry || files.has(entry.file)) return;
    files.add(entry.file);
    for (const dep of entry.imports ?? []) visit(dep);
  };
  visit(src);
  return [...files].map((f) => `<link rel="modulepreload" crossorigin href="/${f}" />`).join("\n    ");
}

function pageHtml(template, { route, body, title, url, jsonLd, preloads, guard }) {
  let html = template;
  html = replaceOnce(html, /<title>[^<]*<\/title>/, `<title>${escapeAttr(title)}</title>`, "<title>");
  html = metaReplace(html, "name", "description", route.description);
  html = metaReplace(html, "property", "og:title", title);
  html = metaReplace(html, "property", "og:description", route.description);
  html = metaReplace(html, "property", "og:url", url);
  html = metaReplace(html, "name", "twitter:title", title);
  html = metaReplace(html, "name", "twitter:description", route.description);
  html = replaceOnce(html, /<link rel="canonical" href="[^"]*" \/>/, `<link rel="canonical" href="${url}" />`, "canonical link");
  html = replaceOnce(
    html,
    "</head>",
    `    ${preloads}\n    <script type="application/ld+json">${jsonLd}</script>\n  </head>`,
    "</head>",
  );
  html = replaceOnce(
    html,
    ROOT_EMPTY,
    `<div id="root" data-prerendered="${route.path}">${body}</div>${guard}`,
    "empty #root",
  );
  return html;
}

function checkBody(route, body) {
  if (!body.trim()) throw new Error(`prerender: ${route.path} rendered empty`);
  if (!body.includes("<h1")) throw new Error(`prerender: ${route.path} has no <h1>`);
  if (body.includes(NOT_FOUND_TEXT)) {
    throw new Error(`prerender: ${route.path} rendered the 404 page; is it missing from src/app.tsx?`);
  }
  for (const marker of STREAMING_MARKERS) {
    if (body.includes(marker)) {
      throw new Error(`prerender: ${route.path} contains streamed-in content (${marker}); crawlers would see a spinner`);
    }
  }
}

async function main() {
  if (!existsSync(MANIFEST)) throw new Error(`prerender: ${MANIFEST} missing; build.manifest must be on`);
  const manifest = JSON.parse(readFileSync(MANIFEST, "utf-8"));
  const server = await import(pathToFileURL(join(SERVER_DIR, "entry-server.js")).href);
  const template = readFileSync(join(DIST, "index.html"), "utf-8");
  const site = server.SITE_URL;

  for (const route of server.ROUTE_LIST) {
    const body = await server.render(route.path);
    checkBody(route, body);
    const title = server.documentTitle(route);
    const url = route.path === "/" ? `${site}/` : `${site}${route.path}`;
    const html = pageHtml(template, {
      route,
      body,
      title,
      url,
      jsonLd: server.structuredData(route, title),
      preloads: preloadsFor(manifest, route),
      guard: mismatchGuard(server.normalizePath),
    });
    const out = route.path === "/" ? join(DIST, "index.html") : join(DIST, route.path, "index.html");
    mkdirSync(dirname(out), { recursive: true });
    writeFileSync(out, html);
    console.log(`prerender: ${route.path} -> ${out.replace(DIST, "dist")} (${(html.length / 1024).toFixed(0)} KB)`);
  }

  // lastmod is when the site's source last changed, not when it was built: a
  // date that moves on every build teaches search engines to ignore it.
  const lastmod = execFileSync("git", ["log", "-1", "--format=%cs", "--", "."], {
    cwd: resolve(HERE, ".."),
    encoding: "utf-8",
  }).trim();
  if (!/^\d{4}-\d{2}-\d{2}$/.test(lastmod)) throw new Error(`prerender: bad git date "${lastmod}" for sitemap lastmod`);
  const urls = server.ROUTE_LIST.map((r) => {
    const loc = r.path === "/" ? `${site}/` : `${site}${r.path}`;
    return `  <url>\n    <loc>${loc}</loc>\n    <lastmod>${lastmod}</lastmod>\n  </url>`;
  }).join("\n");
  writeFileSync(
    join(DIST, "sitemap.xml"),
    `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>\n`,
  );
  // The docs are unlisted, not secret: they carry <meta name="robots"
  // content="noindex">, which crawlers can only honour if they may fetch them.
  writeFileSync(join(DIST, "robots.txt"), `User-agent: *\nAllow: /\n\nSitemap: ${site}/sitemap.xml\n`);

  // Build-only artefacts: the manifest and the server bundle are not the site.
  rmSync(join(DIST, ".vite"), { recursive: true, force: true });
  rmSync(SERVER_DIR, { recursive: true, force: true });
  console.log(`prerender: ${server.ROUTE_LIST.length} pages, sitemap.xml, robots.txt`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
