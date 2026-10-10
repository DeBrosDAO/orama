// Emits the agent-facing docs endpoint into the built site:
//   dist/llms.txt        — llmstxt.org index (what an LLM reads first)
//   dist/llms/<slug>.md  — Markdown of each docs page, <persona>-<page>.md
//
// Source of truth is the website's own docs: every src/docs/<persona>/<page>.mdx
// the navigation publishes. The MDX is reduced to plain Markdown (front matter,
// imports, exports and JSX tags dropped; everything else, code fences
// included, kept as written). Titles and descriptions come from the page list
// the prerenderer already builds, so the index says what the site says. A page
// the navigation does not list, or a listed page with no file, fails the build
// loudly (no silent skip).

import { mkdirSync, readdirSync, readFileSync, writeFileSync, copyFileSync, existsSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const SRC_DOCS = resolve(HERE, "../src/docs");
const DIST = resolve(HERE, "../dist");
const BLOG = resolve(HERE, "../blog");
const SERVER_ENTRY = resolve(HERE, "../dist-server/entry-server.js");
const BASE = "https://orama.network";
const DOCS_URL_PREFIX = "/docs/";

const PROJECT = "Orama Network";
const SUMMARY =
  "Orama Network is a decentralized platform for deploying web apps, " +
  "SQLite databases and serverless WASM functions across a peer-to-peer node " +
  "network, reached through a single API gateway per namespace. A separate " +
  "Cosmos SDK ledger, oramad, mints the ORAMA token. Application requests do " +
  "not pass through that ledger. Custom domains can be verified, but " +
  "certificates are issued only on the network's own domain.";

// persona directory -> section heading, in the order the index lists them
const SECTIONS = [
  ["start", "Start here"],
  ["developer", "Developer"],
  ["operator", "Operator"],
  ["architecture", "Architecture and security"],
  ["blockchain", "Blockchain"],
  ["privacy", "Privacy network"],
  ["rootwallet", "RootWallet"],
  ["contributor", "Contributor"],
];

const FENCE = /^\s*(`{3,}|~{3,})/;
const JSX_OPEN = /^\s*<\/?[A-Za-z][\w.]*(\s|\/?>|$)/;
const ESM_LINE = /^(import|export)\s.*\sfrom\s+["'][^"']+["'];?\s*$|^export\s+default\s/;

/** MDX to plain Markdown: drops front matter, ESM lines and JSX tags outside code fences. */
export function mdxToMarkdown(source) {
  const lines = source.replace(/\r\n/g, "\n").split("\n");
  const out = [];
  let i = 0;
  if (lines[0] === "---") {
    const end = lines.indexOf("---", 1);
    if (end > 0) i = end + 1;
  }
  let fence = null;
  for (; i < lines.length; i++) {
    const line = lines[i];
    const m = FENCE.exec(line);
    if (m) {
      if (fence === null) fence = m[1][0].repeat(m[1].length);
      else if (line.trim().startsWith(fence) && line.trim().replace(/[`~]/g, "") === "") fence = null;
      out.push(line);
      continue;
    }
    if (fence !== null) {
      out.push(line);
      continue;
    }
    if (ESM_LINE.test(line)) continue;
    if (JSX_OPEN.test(line)) {
      // A tag may span lines (an <iframe> with attributes): skip to the line that ends it.
      while (i < lines.length - 1 && !/>\s*$/.test(lines[i])) i++;
      continue;
    }
    out.push(line);
  }
  return out.join("\n").replace(/\n{3,}/g, "\n\n").trim() + "\n";
}

/** Every docs page file as a slug ("developer/functions"), sorted. */
function docFiles() {
  const slugs = [];
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.name.endsWith(".mdx")) slugs.push(relative(SRC_DOCS, full).split(sep).join("/").replace(/\.mdx$/, ""));
    }
  };
  walk(SRC_DOCS);
  return slugs.sort();
}

/** The published blog posts, newest first, as raw Markdown next to the docs. */
async function blogLines(llmsDir) {
  const { POSTS } = await import(pathToFileURL(SERVER_ENTRY).href);
  if (POSTS.length === 0) return { lines: [], count: 0 };
  mkdirSync(join(llmsDir, "blog"), { recursive: true });
  const lines = ["## Blog", ""];
  for (const post of POSTS) {
    copyFileSync(join(BLOG, `${post.slug}.md`), join(llmsDir, "blog", `${post.slug}.md`));
    lines.push(`- [${post.title}](${BASE}/llms/blog/${post.slug}.md): ${post.description}`);
  }
  lines.push("");
  return { lines, count: POSTS.length };
}

async function build() {
  const llmsDir = join(DIST, "llms");
  mkdirSync(llmsDir, { recursive: true });

  const { PAGES } = await import(pathToFileURL(SERVER_ENTRY).href);
  const published = new Map(
    PAGES.filter((p) => p.path.startsWith(DOCS_URL_PREFIX)).map((p) => [p.path.slice(DOCS_URL_PREFIX.length), p]),
  );
  const files = docFiles();
  for (const slug of files) {
    if (!published.has(slug)) {
      throw new Error(`build-llms: src/docs/${slug}.mdx is not in the docs navigation (src/data/docs-navigation.ts)`);
    }
  }
  for (const slug of published.keys()) {
    if (!existsSync(join(SRC_DOCS, `${slug}.mdx`))) {
      throw new Error(`build-llms: the navigation lists "${slug}" but src/docs/${slug}.mdx is missing`);
    }
  }

  const lines = [`# ${PROJECT}`, "", `> ${SUMMARY}`, ""];
  let count = 0;
  for (const [persona, heading] of SECTIONS) {
    const slugs = [...published.keys()].filter((slug) => slug.startsWith(`${persona}/`));
    if (slugs.length === 0) throw new Error(`build-llms: no docs page for the "${persona}" section`);
    lines.push(`## ${heading}`, "");
    for (const slug of slugs) {
      const page = published.get(slug);
      const name = slug.replace("/", "-");
      writeFileSync(join(llmsDir, `${name}.md`), mdxToMarkdown(readFileSync(join(SRC_DOCS, `${slug}.mdx`), "utf8")));
      lines.push(`- [${page.card?.title ?? page.title}](${BASE}/llms/${name}.md): ${page.description}`);
      count++;
    }
    lines.push("");
  }

  const blog = await blogLines(llmsDir);
  lines.push(...blog.lines);

  writeFileSync(join(DIST, "llms.txt"), lines.join("\n"));
  console.log(`build-llms: wrote llms.txt + ${count} docs + ${blog.count} posts to ${llmsDir}`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  build().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
