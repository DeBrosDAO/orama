/// <reference types="vite/client" />

declare module "*.png" {
  const value: string;
  export default value;
}

declare module "*.svg" {
  const value: string;
  export default value;
}

declare module "virtual:docs-search-index" {
  interface SectionEntry {
    pageTitle: string;
    pageSlug: string;
    sectionTitle: string;
    sectionId: string;
    persona: "developer" | "operator" | "contributor" | "blockchain";
  }
  export const SECTION_INDEX: SectionEntry[];
}

/** Injected by vite.config.ts from git at build time. */
declare const __REPO_COMMITS__: number;
declare const __REPO_FIRST_COMMIT__: string;

declare module "*.md" {
  import type { ComponentType } from "react";
  const Component: ComponentType;
  export default Component;
}

declare module "*.jpg" {
  const value: string;
  export default value;
}
declare const __BUILD_YEAR__: number;

declare module "virtual:blog-posts" {
  import type { ComponentType } from "react";
  import type { BlogPost } from "./blog/parse-post";
  /** Published posts, newest first (src/blog/vite-plugin.ts). */
  export const POSTS: BlogPost[];
  /** The body of each post in POSTS, as a lazily loaded component. */
  export const LOADERS: Record<string, () => Promise<{ default: ComponentType }>>;
}

declare module "virtual:docs-meta" {
  import type { DocMeta } from "./lib/doc-meta";
  /** Title and search description of every docs page, keyed by slug. */
  export const DOC_META: Record<string, DocMeta>;
}
