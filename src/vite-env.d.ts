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
