import { SITE_NAME, SITE_URL } from "../content/site";
import { BLOG_FEED_PATH, BLOG_PATH, lastModified, postPath } from "./posts";
import type { BlogPost } from "./posts";

/**
 * The blog's RSS 2.0 feed. Feed readers subscribe to it, and Google Search
 * Console accepts it as a sitemap, so new posts are picked up quickly.
 * Built from the posts alone, so it changes only when a post does.
 */

/** Feed readers only need the recent posts; the sitemap lists them all. */
export const FEED_LIMIT = 50;

const escapeXml = (s: string) =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&apos;");

/** RFC 822 date, as RSS requires, at midnight UTC of a YYYY-MM-DD day. */
export const rfc822 = (day: string) => new Date(`${day}T00:00:00Z`).toUTCString();

function item(post: BlogPost): string {
  const url = `${SITE_URL}${postPath(post.slug)}`;
  const categories = post.tags.map((t) => `      <category>${escapeXml(t)}</category>`).join("\n");
  return [
    "    <item>",
    `      <title>${escapeXml(post.title)}</title>`,
    `      <link>${url}</link>`,
    `      <guid isPermaLink="true">${url}</guid>`,
    `      <description>${escapeXml(post.description)}</description>`,
    `      <pubDate>${rfc822(post.date)}</pubDate>`,
    categories,
    "    </item>",
  ]
    .filter(Boolean)
    .join("\n");
}

export function rssFeed(posts: BlogPost[]): string {
  const recent = posts.slice(0, FEED_LIMIT);
  const newest = recent.map(lastModified).sort().at(-1);
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">',
    "  <channel>",
    `    <title>${escapeXml(SITE_NAME)} blog</title>`,
    `    <link>${SITE_URL}${BLOG_PATH}</link>`,
    `    <atom:link href="${SITE_URL}${BLOG_FEED_PATH}" rel="self" type="application/rss+xml" />`,
    "    <description>Articles from the Orama Network team on decentralized cloud infrastructure, privacy and self-hosting.</description>",
    "    <language>en</language>",
    ...(newest ? [`    <lastBuildDate>${rfc822(newest)}</lastBuildDate>`] : []),
    ...recent.map(item),
    "  </channel>",
    "</rss>",
    "",
  ].join("\n");
}
