// Checks the docs without a full site build:
//   - every src/docs/**/*.mdx compiles as MDX (with the same GFM plugin the site uses)
//   - every /docs/... link points at a page that exists
//   - every page is listed in src/data/docs-navigation.ts, and every listed slug has a file
//
//   node scripts/check-docs.mjs
//
// Exits 1 with a list of problems. Run it before committing docs.

import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import remarkGfm from "remark-gfm";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// @mdx-js/mdx is a dependency of @mdx-js/rollup (what the site builds with), not
// of this package: resolve it from there so the check compiles with the same copy.
const require = createRequire(import.meta.url);
const mdxPath = require.resolve("@mdx-js/mdx", { paths: [dirname(require.resolve("@mdx-js/rollup"))] });
const { compile } = await import(pathToFileURL(mdxPath).href);

const HERE = dirname(fileURLToPath(import.meta.url));
const DOCS = resolve(HERE, "../src/docs");
const NAV = resolve(HERE, "../src/data/docs-navigation.ts");

function walk(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? walk(path) : path.endsWith(".mdx") ? [path] : [];
  });
}

const files = walk(DOCS);
const slugs = new Set(files.map((f) => relative(DOCS, f).replace(/\.mdx$/, "").split("\\").join("/")));
const problems = [];

for (const file of files) {
  const source = readFileSync(file, "utf-8");
  const name = relative(DOCS, file);
  try {
    await compile(source, { remarkPlugins: [remarkGfm] });
  } catch (err) {
    problems.push(`${name}: does not compile: ${String(err.message).split("\n")[0]}`);
    continue;
  }
  // Links like (/docs/operator/install-from-scratch#verify) outside code fences.
  const prose = source.replace(/```[\s\S]*?```/g, "");
  for (const m of prose.matchAll(/\]\((\/docs\/[^)\s]*)\)/g)) {
    const [path, anchor] = m[1].replace(/^\/docs\//, "").split("#");
    const target = path.replace(/\/$/, "");
    if (!slugs.has(target)) problems.push(`${name}: link to missing page /docs/${target}`);
    else if (anchor) {
      const targetText = readFileSync(join(DOCS, `${target}.mdx`), "utf-8");
      const ids = [...targetText.replace(/```[\s\S]*?```/g, "").matchAll(/^#{2,3}\s+(.+)$/gm)].map((h) =>
        h[1].toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/(^-|-$)/g, ""),
      );
      if (!ids.includes(anchor)) problems.push(`${name}: link to missing section /docs/${target}#${anchor}`);
    }
  }
}

const nav = readFileSync(NAV, "utf-8");
const listed = new Set([...nav.matchAll(/slug:\s*"([^"]+)"/g)].map((m) => m[1]));
for (const s of slugs) if (!listed.has(s)) problems.push(`${s}: page is not in docs-navigation.ts`);
for (const s of listed) if (!slugs.has(s)) problems.push(`${s}: listed in docs-navigation.ts but has no file`);

if (problems.length > 0) {
  console.error(problems.join("\n"));
  console.error(`\ncheck-docs: ${problems.length} problem(s) in ${files.length} pages`);
  process.exit(1);
}
console.log(`check-docs: ${files.length} pages ok`);
