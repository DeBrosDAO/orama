import { absoluteUrl } from "./seo";
import type { PageMeta, SitemapGroup } from "./pages";

/**
 * The sitemaps: one per part of the site (pages, docs, blog) and an index
 * at /sitemap.xml naming them, so each part can be watched on its own in
 * Search Console and none nears the 50,000-URL limit. lastmod is the date
 * the page's content last changed; Google ignores priority and changefreq,
 * so they are left out.
 */

export const SITEMAP_GROUPS: SitemapGroup[] = ["pages", "docs", "blog"];
export const sitemapPath = (group: SitemapGroup) => `/sitemap-${group}.xml`;

const HEADER = '<?xml version="1.0" encoding="UTF-8"?>';
const NS = 'xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"';

export function sitemapXml(pages: PageMeta[], lastmod: (page: PageMeta) => string): string {
  const urls = pages.map(
    (p) => `  <url>\n    <loc>${absoluteUrl(p.path)}</loc>\n    <lastmod>${lastmod(p)}</lastmod>\n  </url>`,
  );
  return `${HEADER}\n<urlset ${NS}>\n${urls.join("\n")}\n</urlset>\n`;
}

/** The index, dated by the newest page in each sitemap. */
export function sitemapIndexXml(groups: { group: SitemapGroup; lastmod: string }[]): string {
  const items = groups.map(
    ({ group, lastmod }) =>
      `  <sitemap>\n    <loc>${absoluteUrl(sitemapPath(group))}</loc>\n    <lastmod>${lastmod}</lastmod>\n  </sitemap>`,
  );
  return `${HEADER}\n<sitemapindex ${NS}>\n${items.join("\n")}\n</sitemapindex>\n`;
}
