import { StrictMode } from "react";
import { prerender } from "react-dom/static";
import { StaticRouter } from "react-router";
import { App } from "./app";
import { normalizePath } from "./content/routes";
import { PAGES, knownLastModified } from "./content/pages";
import type { PageMeta } from "./content/pages";
import { INVESTOR_PDF, SITE_URL } from "./content/site";
import { ROBOTS_INDEX, ROBOTS_NOINDEX, absoluteUrl, extraHeadTags, structuredData } from "./content/seo";
import { SITEMAP_GROUPS, sitemapIndexXml, sitemapPath, sitemapXml } from "./content/sitemap";
import { BLOG_FEED_PATH, POSTS } from "./blog/posts";
import { rssFeed } from "./blog/feed";

export {
  BLOG_FEED_PATH,
  INVESTOR_PDF,
  PAGES,
  POSTS,
  ROBOTS_INDEX,
  ROBOTS_NOINDEX,
  SITEMAP_GROUPS,
  SITE_URL,
  absoluteUrl,
  extraHeadTags,
  knownLastModified,
  normalizePath,
  rssFeed,
  sitemapIndexXml,
  sitemapPath,
  sitemapXml,
  structuredData,
};
export type { PageMeta };

/** Any address no page owns: rendering it gives the 404 page. */
export const NOT_FOUND_PROBE = "/404";

/**
 * Render one page to HTML at build time. prerender (unlike renderToString)
 * waits for every lazy page and Suspense boundary to resolve, so the output
 * is the finished page, not a loading spinner.
 *
 * progressiveChunkSize: by default React 19 streams any Suspense boundary
 * larger than ~12 KB "out of order": the fallback spinner goes in place, the
 * real page into a hidden <div>, and a script swaps them after the next frame.
 * For a static page that means crawlers and first paint see a spinner. An
 * infinite chunk size keeps every boundary inline.
 */
export async function render(url: string): Promise<string> {
  const { prelude } = await prerender(
    <StrictMode>
      <StaticRouter location={url}>
        <App />
      </StaticRouter>
    </StrictMode>,
    { progressiveChunkSize: Number.POSITIVE_INFINITY },
  );
  return new Response(prelude).text();
}
