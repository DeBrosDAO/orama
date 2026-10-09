import GithubSlugger from "github-slugger";
import { parse as parseYaml } from "yaml";

/**
 * The rules a blog post must follow, checked at build time. A post that
 * breaks one fails the build with a message naming the file and the fix:
 * a post that ships with a missing description or a broken date costs search
 * traffic silently, for as long as nobody notices.
 *
 * Runs in Node (the Vite plugin) only; the browser gets the parsed result.
 */

export interface BlogHeading {
  depth: 2 | 3;
  text: string;
  /** The id rehype-slug gives the rendered heading, for in-page links. */
  id: string;
}

export interface BlogPost {
  slug: string;
  title: string;
  /** Shorter title for the browser tab and search results, when title is too long. */
  seoTitle?: string;
  description: string;
  /** YYYY-MM-DD */
  date: string;
  /** YYYY-MM-DD, on or after date */
  updated?: string;
  tags: string[];
  /** Cover image under /images/blog/, with its size and alt text. */
  cover?: { src: string; alt: string; width: number; height: number };
  draft: boolean;
  wordCount: number;
  readingMinutes: number;
  headings: BlogHeading[];
}

/** Frontmatter as written, before the cover's size is read from disk. */
export type ParsedPost = Omit<BlogPost, "cover"> & { cover?: { src: string; alt: string } };

export const SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/;
const COVER_PATTERN = /^\/images\/blog\/[a-z0-9][a-z0-9/_-]*\.(png|jpe?g|webp)$/;

/** Search results cut titles off past ~60 characters and descriptions past ~160. */
export const MAX_SEARCH_TITLE = 60;
const MIN_DESCRIPTION = 50;
const MAX_DESCRIPTION = 160;
const MIN_TITLE = 10;
const MAX_TITLE = 110;
const MAX_TAGS = 6;
/** Average adult silent reading speed for non-fiction, words per minute. */
const WORDS_PER_MINUTE = 230;

const KNOWN_KEYS = new Set(["title", "seoTitle", "description", "date", "updated", "tags", "cover", "coverAlt", "draft"]);

export class BlogPostError extends Error {
  constructor(slug: string, problem: string) {
    super(`blog post "${slug}": ${problem}`);
    this.name = "BlogPostError";
  }
}

function splitFrontmatter(slug: string, raw: string): { data: unknown; body: string } {
  const text = raw.replace(/^﻿/, "").replace(/\r\n/g, "\n");
  const match = /^---\n([\s\S]*?)\n---(?:\n|$)/.exec(text);
  if (!match) throw new BlogPostError(slug, "must start with a --- frontmatter block (see docs/WEBSITE_BLOG.md)");
  let data: unknown;
  try {
    data = parseYaml(match[1]);
  } catch (err) {
    throw new BlogPostError(slug, `frontmatter is not valid YAML: ${(err as Error).message}`);
  }
  return { data, body: text.slice(match[0].length) };
}

function isRealDate(s: string): boolean {
  if (!DATE_PATTERN.test(s)) return false;
  const d = new Date(`${s}T00:00:00Z`);
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === s;
}

function requireString(slug: string, data: Record<string, unknown>, key: string): string {
  const v = data[key];
  if (typeof v !== "string" || !v.trim()) throw new BlogPostError(slug, `"${key}" is required and must be text`);
  return v.trim();
}

function optionalString(slug: string, data: Record<string, unknown>, key: string): string | undefined {
  const v = data[key];
  if (v === undefined) return undefined;
  if (typeof v !== "string" || !v.trim()) throw new BlogPostError(slug, `"${key}" must be text when set`);
  return v.trim();
}

function checkLength(slug: string, key: string, value: string, min: number, max: number) {
  if (value.length < min || value.length > max) {
    throw new BlogPostError(slug, `"${key}" is ${value.length} characters; keep it between ${min} and ${max}`);
  }
}

function parseDate(slug: string, data: Record<string, unknown>, key: string, required: boolean): string | undefined {
  const v = data[key];
  if (v === undefined && !required) return undefined;
  if (typeof v !== "string" || !isRealDate(v)) {
    throw new BlogPostError(slug, `"${key}" must be a quoted-or-plain date like 2026-10-09`);
  }
  return v;
}

function parseTags(slug: string, data: Record<string, unknown>): string[] {
  const v = data.tags;
  if (!Array.isArray(v) || v.length === 0 || v.length > MAX_TAGS) {
    throw new BlogPostError(slug, `"tags" must be a list of 1 to ${MAX_TAGS} tags`);
  }
  const tags = v.map((t) => {
    if (typeof t !== "string" || !SLUG_PATTERN.test(t)) {
      throw new BlogPostError(slug, `tag ${JSON.stringify(t)} must be lowercase words joined by hyphens, like "privacy" or "web3-backends"`);
    }
    return t;
  });
  if (new Set(tags).size !== tags.length) throw new BlogPostError(slug, `"tags" lists a tag twice`);
  return tags;
}

function parseCover(slug: string, data: Record<string, unknown>): ParsedPost["cover"] {
  const src = optionalString(slug, data, "cover");
  const alt = optionalString(slug, data, "coverAlt");
  if (!src && !alt) return undefined;
  if (!src) throw new BlogPostError(slug, `"coverAlt" is set without "cover"`);
  if (!alt) throw new BlogPostError(slug, `"cover" needs "coverAlt": a sentence describing the image`);
  if (!COVER_PATTERN.test(src)) {
    throw new BlogPostError(slug, `"cover" must be a .png, .jpg or .webp under /images/blog/, got ${src}`);
  }
  return { src, alt };
}

/** Link and image targets a post may use: the web, mail, site paths and in-page anchors. */
const SAFE_LINK = /^(https?:\/\/|mailto:|\/|#)/i;

function checkLinks(slug: string, lines: string[]) {
  for (const line of lines) {
    for (const m of line.matchAll(/\]\(\s*<?([^)\s>]*)/g)) {
      if (!SAFE_LINK.test(m[1])) {
        throw new BlogPostError(slug, `link or image "${m[1]}" must start with https://, http://, mailto:, / or #`);
      }
    }
  }
}

/** Markdown inline syntax removed, leaving the text a reader sees. */
export function plainText(md: string): string {
  return md
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/\*\*(\S(?:.*?\S)?)\*\*/g, "$1")
    .replace(/(^|\W)__(\S(?:.*?\S)?)__(?=\W|$)/g, "$1$2")
    .replace(/\*(\S(?:.*?\S)?)\*/g, "$1")
    // Underscore emphasis only at word edges, as CommonMark: snake_case stays.
    .replace(/(^|\W)_(\S(?:.*?\S)?)_(?=\W|$)/g, "$1$2")
    .replace(/<[^>]+>/g, "")
    .trim();
}

/** Body lines outside fenced code blocks. */
function proseLines(body: string): string[] {
  const out: string[] = [];
  let fence: string | null = null;
  for (const line of body.split("\n")) {
    const m = /^\s*(`{3,}|~{3,})/.exec(line);
    if (m) {
      if (fence === null) fence = m[1][0];
      else if (m[1][0] === fence) fence = null;
      continue;
    }
    if (fence === null) out.push(line);
  }
  return out;
}

function parseHeadings(slug: string, lines: string[]): BlogHeading[] {
  // One slugger over every heading in order, as rehype-slug does, so
  // repeated headings get the same -1, -2 suffixes the page renders.
  const slugger = new GithubSlugger();
  const headings: BlogHeading[] = [];
  for (const line of lines) {
    const m = /^(#{1,6})\s+(.+?)\s*#*\s*$/.exec(line);
    if (!m) continue;
    const depth = m[1].length;
    if (depth === 1) {
      throw new BlogPostError(slug, `the body has a "# ${m[2]}" heading; the page already shows the title as the one H1, start sections at ##`);
    }
    const text = plainText(m[2]);
    const id = slugger.slug(text);
    if (depth === 2 || depth === 3) headings.push({ depth, text, id });
  }
  return headings;
}

/** Words of prose; code blocks are skimmed, not read, so they don't count. */
function countWords(lines: string[]): number {
  return plainText(lines.join("\n")).split(/\s+/).filter((w) => /\w/.test(w)).length;
}

/** Parses and checks one post. slug is the file name without .md. */
export function parsePost(slug: string, raw: string): ParsedPost {
  if (!SLUG_PATTERN.test(slug)) {
    throw new BlogPostError(slug, "the file name must be lowercase words joined by hyphens, like why-orama.md");
  }
  const { data, body } = splitFrontmatter(slug, raw);
  if (!data || typeof data !== "object" || Array.isArray(data)) {
    throw new BlogPostError(slug, "frontmatter must be a set of key: value lines");
  }
  const fm = data as Record<string, unknown>;
  for (const key of Object.keys(fm)) {
    if (!KNOWN_KEYS.has(key)) {
      throw new BlogPostError(slug, `unknown frontmatter key "${key}"; allowed: ${[...KNOWN_KEYS].join(", ")}`);
    }
  }

  const title = requireString(slug, fm, "title");
  checkLength(slug, "title", title, MIN_TITLE, MAX_TITLE);
  const seoTitle = optionalString(slug, fm, "seoTitle");
  if (seoTitle) checkLength(slug, "seoTitle", seoTitle, MIN_TITLE, MAX_SEARCH_TITLE);
  if (!seoTitle && title.length > MAX_SEARCH_TITLE) {
    throw new BlogPostError(slug, `"title" is ${title.length} characters, longer than search results show (${MAX_SEARCH_TITLE}); add a shorter "seoTitle"`);
  }
  const description = requireString(slug, fm, "description");
  checkLength(slug, "description", description, MIN_DESCRIPTION, MAX_DESCRIPTION);

  const date = parseDate(slug, fm, "date", true) as string;
  const updated = parseDate(slug, fm, "updated", false);
  if (updated && updated < date) throw new BlogPostError(slug, `"updated" (${updated}) is before "date" (${date})`);

  if (fm.draft !== undefined && typeof fm.draft !== "boolean") {
    throw new BlogPostError(slug, `"draft" must be true or false`);
  }

  const lines = proseLines(body);
  checkLinks(slug, lines);
  const headings = parseHeadings(slug, lines);
  const wordCount = countWords(lines);
  if (wordCount === 0) throw new BlogPostError(slug, "the post has no text");

  return {
    slug,
    title,
    seoTitle,
    description,
    date,
    updated,
    tags: parseTags(slug, fm),
    cover: parseCover(slug, fm),
    draft: fm.draft === true,
    wordCount,
    readingMinutes: Math.max(1, Math.ceil(wordCount / WORDS_PER_MINUTE)),
    headings,
  };
}

/** A post goes out once it is not a draft and its date has come. */
export function isPublished(post: Pick<BlogPost, "draft" | "date">, today: string): boolean {
  return !post.draft && post.date <= today;
}

/** Newest first; posts on the same day by slug, so the order never shuffles between builds. */
export function comparePosts(a: Pick<BlogPost, "date" | "slug">, b: Pick<BlogPost, "date" | "slug">): number {
  return a.date === b.date ? a.slug.localeCompare(b.slug) : b.date.localeCompare(a.date);
}
