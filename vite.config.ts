import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import mdx from "@mdx-js/rollup";
import remarkGfm from "remark-gfm";
import rehypeSlug from "rehype-slug";
import fs from "node:fs";
import path from "node:path";
import type { Plugin } from "vite";

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
