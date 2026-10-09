import { DOC_META } from "virtual:docs-meta";
import {
  BLOG_PATH,
  POSTS,
  allTags,
  blogPagePath,
  lastModified,
  pageCount,
  postPath,
  postsWithTag,
  tagLabel,
  tagPath,
} from "../blog/posts";
import type { BlogPost } from "../blog/posts";
import { ALL_DOCS, PERSONA_FIRST_SLUG } from "../data/docs-navigation";
import type { Persona } from "../types/persona";
import { ROUTES, documentTitle, normalizePath } from "./routes";
import type { RouteKey } from "./routes";

/**
 * Every page the build writes as HTML, and what search engines and link
 * previews are told about it: title, description, breadcrumb trail, preview
 * image, schema.org type, and the source chunks it is built from. The
 * prerenderer, the sitemap, the feeds and each page's <head> all read this
 * one list.
 */

export interface Crumb {
  name: string;
  path: string;
}

export type SchemaType = "WebPage" | "TechArticle" | "BlogPosting" | "CollectionPage";

export type SitemapGroup = "pages" | "docs" | "blog";

export interface PageMeta {
  path: string;
  /** Full browser-tab and search-result title. */
  title: string;
  description: string;
  schema: SchemaType;
  /** Home first, this page last. */
  crumbs: Crumb[];
  /** Link-preview image (site path) and its alt text. */
  image: { path: string; alt: string };
  /** For the generated preview images: the small label above the title. */
  card?: { eyebrow: string; title: string };
  /** Source modules the page is rendered from, as Vite names them. */
  chunks: string[];
  /** null: kept out of the sitemaps (and noindex'd). */
  sitemap: SitemapGroup | null;
  /** Kept out of search results, e.g. a tag page too thin to stand on its own. */
  noindex?: boolean;
  post?: BlogPost;
}

export const HOME_CRUMB: Crumb = { name: "Home", path: "/" };
export const DEFAULT_IMAGE = { path: "/og-image.png", alt: "Orama Network: the cloud, with nobody in the middle." };
/** Generated 1200x630 preview images (scripts/build-og.mjs). */
export const OG_DIR = "/og";
export const OG_SIZE = { width: 1200, height: 630 } as const;

export const DOCS_PATH = "/docs";
export const EXPLORER_PATH = "/explorer";
export const docPath = (slug: string) => `${DOCS_PATH}/${slug}`;

const MAX_TITLE = 60;
/** A tag page lists the same posts as the blog until it has a few of its own. */
export const MIN_POSTS_TO_INDEX_TAG = 3;
const BLOG_DESCRIPTION =
  "Articles from the Orama Network team on decentralized cloud infrastructure, privacy, self-hosting, and what runs on Orama today.";

/** "Name · Suffix" when it fits in a search result, else the name alone. */
export function fitTitle(name: string, suffix: string): string {
  const full = `${name} · ${suffix}`;
  return full.length <= MAX_TITLE ? full : name;
}

export const PERSONA_LABEL: Record<Persona, string> = {
  developer: "Developers",
  operator: "Operators",
  contributor: "Contributors",
  blockchain: "Blockchain",
};

const PAGE_FILE: Record<RouteKey, string> = {
  home: "home",
  platform: "platform",
  howItWorks: "how-it-works",
  useCases: "use-cases",
  apps: "apps",
  roadmap: "roadmap",
  investors: "investors",
  donate: "donate",
  whitepaper: "whitepaper",
};

function staticPages(): PageMeta[] {
  return (Object.keys(ROUTES) as RouteKey[]).map((key) => {
    const route = ROUTES[key];
    const isHome = route.path === "/";
    return {
      path: route.path,
      title: documentTitle(route),
      description: route.description,
      schema: key === "whitepaper" ? "TechArticle" : "WebPage",
      crumbs: isHome ? [HOME_CRUMB] : [HOME_CRUMB, { name: route.title, path: route.path }],
      image: DEFAULT_IMAGE,
      chunks: [`src/pages/${PAGE_FILE[key]}.tsx`],
      sitemap: "pages",
    };
  });
}

const EXPLORER: PageMeta = {
  path: EXPLORER_PATH,
  title: "Explorer: the Orama chain, live · Orama Network",
  description:
    "Browse the Orama chain as it runs: latest blocks, transactions, wallets and validators, read live from the network.",
  schema: "WebPage",
  crumbs: [HOME_CRUMB, { name: "Explorer", path: EXPLORER_PATH }],
  image: DEFAULT_IMAGE,
  chunks: ["src/pages/explorer.tsx"],
  sitemap: "pages",
};

const DOCS_CRUMB: Crumb = { name: "Docs", path: DOCS_PATH };

function docsPages(): PageMeta[] {
  const home: PageMeta = {
    path: DOCS_PATH,
    title: "Documentation · Orama Network",
    description:
      "Orama Network documentation for developers, node operators, contributors and the chain: deploy apps, databases, storage and functions.",
    schema: "CollectionPage",
    crumbs: [HOME_CRUMB, DOCS_CRUMB],
    image: DEFAULT_IMAGE,
    chunks: ["src/pages/docs-home.tsx"],
    sitemap: "docs",
  };
  const pages = ALL_DOCS.map(({ link, persona }): PageMeta => {
    const meta = DOC_META[link.slug];
    if (!meta) throw new Error(`docs: navigation lists "${link.slug}" but src/docs/${link.slug}.mdx does not exist`);
    const path = docPath(link.slug);
    const personaPath = docPath(PERSONA_FIRST_SLUG[persona]);
    const personaCrumb = personaPath === path ? [] : [{ name: PERSONA_LABEL[persona], path: personaPath }];
    return {
      path,
      title: fitTitle(meta.title, "Orama Docs"),
      description: meta.description,
      schema: "TechArticle",
      crumbs: [HOME_CRUMB, DOCS_CRUMB, ...personaCrumb, { name: link.title, path }],
      image: { path: `${OG_DIR}/docs/${link.slug.replace(/\//g, "-")}.png`, alt: meta.title },
      card: { eyebrow: `Docs · ${PERSONA_LABEL[persona]}`, title: meta.title },
      chunks: ["src/pages/docs.tsx", `src/docs/${link.slug}.mdx`],
      sitemap: "docs",
    };
  });
  return [home, ...pages];
}

const BLOG_CRUMB: Crumb = { name: "Blog", path: BLOG_PATH };
const BLOG_IMAGE = { path: `${OG_DIR}/blog.png`, alt: "The Orama Network blog" };

function blogIndexPages(): PageMeta[] {
  return Array.from({ length: pageCount(POSTS.length) }, (_, i): PageMeta => {
    const page = i + 1;
    const path = blogPagePath(page);
    return {
      path,
      title: page === 1 ? "Blog · Orama Network" : `Blog, page ${page} · Orama Network`,
      description:
        page === 1
          ? BLOG_DESCRIPTION
          : `Page ${page} of the Orama Network blog: articles on decentralized cloud infrastructure, privacy and self-hosting.`,
      schema: "CollectionPage",
      crumbs: page === 1 ? [HOME_CRUMB, BLOG_CRUMB] : [HOME_CRUMB, BLOG_CRUMB, { name: `Page ${page}`, path }],
      image: BLOG_IMAGE,
      card: page === 1 ? { eyebrow: "Orama Network", title: "Blog" } : undefined,
      chunks: ["src/pages/blog.tsx"],
      sitemap: "blog",
    };
  });
}

function tagPages(): PageMeta[] {
  return allTags(POSTS).flatMap(({ tag, count }) =>
    Array.from({ length: pageCount(count) }, (_, i): PageMeta => {
      const page = i + 1;
      const label = tagLabel(tag);
      const path = tagPath(tag, page);
      const tagCrumb = { name: label, path: tagPath(tag) };
      return {
        path,
        title: fitTitle(page === 1 ? `${label} articles` : `${label} articles, page ${page}`, "Orama Blog"),
        description: `Articles tagged “${label}” on the Orama Network blog: decentralized cloud infrastructure, privacy and what runs on Orama.`,
        schema: "CollectionPage",
        crumbs: [HOME_CRUMB, BLOG_CRUMB, tagCrumb, ...(page === 1 ? [] : [{ name: `Page ${page}`, path }])],
        image: BLOG_IMAGE,
        chunks: ["src/pages/blog-tag.tsx"],
        sitemap: count >= MIN_POSTS_TO_INDEX_TAG ? "blog" : null,
        noindex: count < MIN_POSTS_TO_INDEX_TAG,
      };
    }),
  );
}

function postPages(): PageMeta[] {
  return POSTS.map((post): PageMeta => {
    const path = postPath(post.slug);
    return {
      path,
      title: fitTitle(post.seoTitle ?? post.title, "Orama Blog"),
      description: post.description,
      schema: "BlogPosting",
      crumbs: [HOME_CRUMB, BLOG_CRUMB, { name: post.title, path }],
      image: { path: `${OG_DIR}/blog/${post.slug}.png`, alt: post.title },
      card: { eyebrow: `Blog · ${tagLabel(post.tags[0])}`, title: post.title },
      chunks: ["src/pages/blog-post.tsx", `blog/${post.slug}.md`],
      sitemap: "blog",
      post,
    };
  });
}

/** Every prerendered page, in sitemap order. */
export const PAGES: PageMeta[] = [
  ...staticPages(),
  EXPLORER,
  ...docsPages(),
  ...blogIndexPages(),
  ...tagPages(),
  ...postPages(),
];

const BY_PATH = new Map(PAGES.map((p) => [p.path, p]));

export const pageFor = (path: string): PageMeta | undefined => BY_PATH.get(normalizePath(path));

/** The date a page's content last changed, when the page knows it itself (posts). */
export function knownLastModified(page: PageMeta): string | undefined {
  if (page.post) return lastModified(page.post);
  if (page.schema === "CollectionPage" && page.path.startsWith(BLOG_PATH)) {
    const tag = /^\/blog\/tag\/([^/]+)/.exec(page.path)?.[1];
    const newest = (tag ? postsWithTag(POSTS, tag) : POSTS).map(lastModified).sort().at(-1);
    return newest;
  }
  return undefined;
}
