import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import mdx from "@mdx-js/rollup";
import remarkGfm from "remark-gfm";
import rehypeSlug from "rehype-slug";
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import type { Plugin } from "vite";

/**
 * Repository facts shown on the site (commit count, first commit date). Read
 * from git at build time so the numbers can't drift from the history. A build
 * outside a git checkout fails here, loudly, rather than shipping made-up
 * numbers.
 */
function readRepoStats() {
  const git = (...args: string[]) =>
    execFileSync("git", args, { cwd: __dirname, encoding: "utf-8" }).trim();
  let shallow: string, commits: number, firstCommit: string;
  try {
    shallow = git("rev-parse", "--is-shallow-repository");
    commits = Number(git("rev-list", "--count", "HEAD"));
    firstCommit = git("log", "--reverse", "--format=%ad", "--date=short").split("\n")[0];
  } catch (err) {
    throw new Error(
      `vite.config: cannot read repository stats from git (build from a git checkout of the orama repo): ${String(err)}`,
    );
  }
  // A shallow clone (CI default) counts only the fetched commits and dates
  // the "first" commit to the newest one: numbers that look real and aren't.
  if (shallow === "true") {
    throw new Error("vite.config: shallow git clone; fetch full history (git fetch --unshallow) to build the site");
  }
  if (!Number.isInteger(commits) || commits < 1 || !/^\d{4}-\d{2}-\d{2}$/.test(firstCommit)) {
    throw new Error(`vite.config: unexpected git output (commits=${commits}, first=${firstCommit})`);
  }
  return { commits, firstCommit };
}

const repoStats = readRepoStats();

/**
 * Vite plugin that generates a search index from MDX heading markers.
 * Exposes a virtual module `virtual:docs-search-index` containing all
 * h2 sections extracted from `src/docs/` MDX files at build time.
 */
function docsSearchIndexPlugin(): Plugin {
  const virtualId = "virtual:docs-search-index";
  const resolvedId = "\0" + virtualId;

  return {
    name: "docs-search-index",
    resolveId(id) {
      if (id === virtualId) return resolvedId;
    },
    load(id) {
      if (id !== resolvedId) return;

      const docsDir = path.resolve(__dirname, "src/docs");
      const entries: {
        pageTitle: string;
        pageSlug: string;
        sectionTitle: string;
        sectionId: string;
        persona: string;
      }[] = [];

      function walk(dir: string) {
        for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
          if (entry.isDirectory()) {
            walk(path.join(dir, entry.name));
          } else if (entry.name.endsWith(".mdx")) {
            const fullPath = path.join(dir, entry.name);
            const relative = path.relative(docsDir, fullPath);
            const slug = relative.replace(/\.mdx$/, "");
            const raw = fs.readFileSync(fullPath, "utf-8");

            let persona = "developer";
            if (slug.startsWith("operator/")) persona = "operator";
            else if (slug.startsWith("contributor/")) persona = "contributor";

            const titleMatch = raw.match(/^#\s+(.+)$/m);
            const pageTitle = titleMatch?.[1] ?? slug;

            const h2Regex = /^##\s+(.+)$/gm;
            let match;
            while ((match = h2Regex.exec(raw)) !== null) {
              const sectionTitle = match[1];
              const sectionId = sectionTitle
                .toLowerCase()
                .replace(/[^a-z0-9]+/g, "-")
                .replace(/(^-|-$)/g, "");
              entries.push({
                pageTitle,
                pageSlug: slug,
                sectionTitle,
                sectionId,
                persona,
              });
            }
          }
        }
      }

      walk(docsDir);

      return `export const SECTION_INDEX = ${JSON.stringify(entries)};`;
    },
  };
}

export default defineConfig({
  define: {
    __REPO_COMMITS__: JSON.stringify(repoStats.commits),
    __REPO_FIRST_COMMIT__: JSON.stringify(repoStats.firstCommit),
    // Fixed at build time so the prerendered footer and the hydrated one agree.
    __BUILD_YEAR__: JSON.stringify(new Date().getUTCFullYear()),
  },
  build: {
    // scripts/prerender.mjs reads it to preload each page's own chunks.
    manifest: true,
  },
  server: {
    // The whitepaper's source lives in the repo-root docs/ tree.
    fs: { allow: [__dirname, path.resolve(__dirname, "../docs/whitepaper")] },
  },
  resolve: {
    // ...and a file out there has no node_modules of its own: resolve the
    // runtime its compiled MDX imports from this package, as one copy.
    dedupe: ["react", "react-dom", "@mdx-js/react"],
  },
  plugins: [
    docsSearchIndexPlugin(),
    {
      enforce: 'pre' as const,
      ...mdx({
        providerImportSource: '@mdx-js/react',
        remarkPlugins: [remarkGfm],
        rehypePlugins: [rehypeSlug],
      }),
    },
    react({ include: /\.(jsx|tsx|mdx)$/ }),
    tailwindcss(),
  ],
});
