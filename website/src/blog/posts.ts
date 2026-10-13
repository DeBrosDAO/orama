import { lazy } from "react";
import type { ComponentType, LazyExoticComponent } from "react";
import { LOADERS, POSTS } from "virtual:blog-posts";
import type { BlogPost } from "./parse-post";

/**
 * The blog as the site sees it: the published posts (newest first), their
 * addresses, tags, pages of the index, and each post's body as a lazy
 * component, so prerendering waits for it and the browser loads one post at
 * a time.
 */

export { POSTS };
export type { BlogPost };

export const BLOG_PATH = "/blog";
export const BLOG_FEED_PATH = "/blog/rss.xml";
export const POSTS_PER_PAGE = 12;

export const postPath = (slug: string) => `${BLOG_PATH}/${slug}`;

export const blogPagePath = (page: number) => (page === 1 ? BLOG_PATH : `${BLOG_PATH}/page/${page}`);

export const tagPath = (tag: string, page = 1) =>
  page === 1 ? `${BLOG_PATH}/tag/${tag}` : `${BLOG_PATH}/tag/${tag}/page/${page}`;

/** "web3-backends" -> "Web3 backends" */
export function tagLabel(tag: string): string {
  const words = tag.replace(/-/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/** The date a post last changed: its update, or its first publication. */
export const lastModified = (post: BlogPost) => post.updated ?? post.date;

/** Every tag in use, most used first. */
export function allTags(posts: BlogPost[]): { tag: string; count: number }[] {
  const counts = new Map<string, number>();
  for (const p of posts) for (const t of p.tags) counts.set(t, (counts.get(t) ?? 0) + 1);
  return [...counts]
    .map(([tag, count]) => ({ tag, count }))
    .sort((a, b) => b.count - a.count || a.tag.localeCompare(b.tag));
}

export const postsWithTag = (posts: BlogPost[], tag: string) => posts.filter((p) => p.tags.includes(tag));

export const pageCount = (total: number) => Math.max(1, Math.ceil(total / POSTS_PER_PAGE));

/** Page n (from 1) of items; empty when n is out of range. */
export function pageOf<T>(items: T[], page: number): T[] {
  if (!Number.isInteger(page) || page < 1) return [];
  return items.slice((page - 1) * POSTS_PER_PAGE, page * POSTS_PER_PAGE);
}

/** Posts sharing the most tags with this one, newer first on a tie. */
export function relatedPosts(posts: BlogPost[], post: BlogPost, limit = 3): BlogPost[] {
  const tags = new Set(post.tags);
  return posts
    .filter((p) => p.slug !== post.slug)
    .map((p, index) => ({ p, index, shared: p.tags.filter((t) => tags.has(t)).length }))
    .filter((x) => x.shared > 0)
    .sort((a, b) => b.shared - a.shared || a.index - b.index)
    .slice(0, limit)
    .map((x) => x.p);
}

/** The posts published just before and just after this one. */
export function adjacentPosts(posts: BlogPost[], slug: string): { newer?: BlogPost; older?: BlogPost } {
  const i = posts.findIndex((p) => p.slug === slug);
  if (i === -1) return {};
  return { newer: posts[i - 1], older: posts[i + 1] };
}

export const findPost = (slug: string) => POSTS.find((p) => p.slug === slug);

const bodies = new Map<string, LazyExoticComponent<ComponentType>>();

/** The post's rendered Markdown; one lazy component per post, created once. */
export function postBody(slug: string): LazyExoticComponent<ComponentType> {
  const load = LOADERS[slug];
  if (!load) throw new Error(`blog: no body for post "${slug}"`);
  let body = bodies.get(slug);
  if (!body) {
    body = lazy(load);
    bodies.set(slug, body);
  }
  return body;
}

/**
 * The page number from a /page/:n address. Page 1 has no /page/1 address
 * (that would be a second URL for the same page), so only 2 and up parse.
 */
export function parsePageParam(raw: string | undefined): number | null {
  if (raw === undefined) return 1;
  return /^[1-9]\d*$/.test(raw) && raw !== "1" ? Number(raw) : null;
}
