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
    persona: "developer" | "operator" | "contributor";
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
