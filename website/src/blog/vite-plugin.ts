import fs from "node:fs";
import path from "node:path";
import { imageSize } from "image-size";
import type { Plugin } from "vite";
import { comparePosts, isPublished, parsePost } from "./parse-post";
import type { BlogPost } from "./parse-post";

/**
 * Serves `virtual:blog-posts`: every post in website/blog/ that is published,
 * parsed and checked, plus a loader per post for its body.
 *
 * Only published posts get a loader, so a draft's text never ships in a
 * chunk of the production build. `vite dev` also shows drafts and posts dated
 * in the future, so authors can preview them.
 */

const VIRTUAL_ID = "virtual:blog-posts";
const RESOLVED_ID = "\0" + VIRTUAL_ID;

function readCover(publicDir: string, post: ReturnType<typeof parsePost>): BlogPost["cover"] {
  if (!post.cover) return undefined;
  const file = path.join(publicDir, post.cover.src);
  if (!fs.existsSync(file)) {
    throw new Error(`blog post "${post.slug}": cover ${post.cover.src} not found (expected at ${file})`);
  }
  const { width, height } = imageSize(fs.readFileSync(file));
  if (!width || !height) throw new Error(`blog post "${post.slug}": cannot read the size of ${post.cover.src}`);
  return { ...post.cover, width, height };
}

/** Every post file in dir, parsed. Throws on the first broken post. */
export function loadPosts(dir: string, publicDir: string): { post: BlogPost; file: string }[] {
  if (!fs.existsSync(dir)) throw new Error(`blog: posts directory ${dir} is missing`);
  return fs
    .readdirSync(dir)
    .filter((name) => name.endsWith(".md"))
    .map((name) => {
      const file = path.join(dir, name);
      const parsed = parsePost(name.slice(0, -3), fs.readFileSync(file, "utf-8"));
      return { post: { ...parsed, cover: readCover(publicDir, parsed) }, file };
    });
}

export function blogPostsPlugin(options: { dir: string; publicDir: string }): Plugin {
  let includeUnpublished = false;
  return {
    name: "blog-posts",
    configResolved(config) {
      includeUnpublished = config.command === "serve" && config.mode !== "test";
    },
    resolveId(id) {
      if (id === VIRTUAL_ID) return RESOLVED_ID;
    },
    load(id) {
      if (id !== RESOLVED_ID) return;
      const today = new Date().toISOString().slice(0, 10);
      const entries = loadPosts(options.dir, options.publicDir)
        .filter(({ post }) => includeUnpublished || isPublished(post, today))
        .sort((a, b) => comparePosts(a.post, b.post));
      const loaders = entries
        .map(({ post, file }) => `  ${JSON.stringify(post.slug)}: () => import(${JSON.stringify(file)}),`)
        .join("\n");
      return (
        `export const POSTS = ${JSON.stringify(entries.map((e) => e.post))};\n` +
        `export const LOADERS = {\n${loaders}\n};\n`
      );
    },
    configureServer(server) {
      // A post added, renamed or deleted changes the list itself.
      const refresh = (file: string) => {
        if (!file.startsWith(options.dir + path.sep)) return;
        const mod = server.moduleGraph.getModuleById(RESOLVED_ID);
        if (mod) server.moduleGraph.invalidateModule(mod);
        server.ws.send({ type: "full-reload" });
      };
      server.watcher.add(options.dir);
      server.watcher.on("add", refresh);
      server.watcher.on("unlink", refresh);
      server.watcher.on("change", refresh);
    },
  };
}
