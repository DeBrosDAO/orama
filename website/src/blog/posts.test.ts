import { describe, expect, it } from "vitest";
import {
  POSTS_PER_PAGE,
  adjacentPosts,
  allTags,
  blogPagePath,
  pageCount,
  pageOf,
  parsePageParam,
  postBody,
  relatedPosts,
  tagLabel,
  tagPath,
} from "./posts";
import type { BlogPost } from "./posts";

const make = (slug: string, date: string, tags: string[]): BlogPost => ({
  slug,
  title: slug,
  description: "",
  date,
  tags,
  draft: false,
  wordCount: 1,
  readingMinutes: 1,
  headings: [],
});

// Newest first, as POSTS is.
const POSTS = [
  make("d", "2026-04-01", ["privacy", "calls"]),
  make("c", "2026-03-01", ["privacy"]),
  make("b", "2026-02-01", ["hosting"]),
  make("a", "2026-01-01", ["privacy", "calls"]),
];

describe("blog lists", () => {
  it("TestAllTags_most_used_first", () => {
    expect(allTags(POSTS)).toEqual([
      { tag: "privacy", count: 3 },
      { tag: "calls", count: 2 },
      { tag: "hosting", count: 1 },
    ]);
    expect(allTags([])).toEqual([]);
  });

  it("TestRelatedPosts_by_shared_tags_then_recency", () => {
    expect(relatedPosts(POSTS, POSTS[0]).map((p) => p.slug)).toEqual(["a", "c"]);
    expect(relatedPosts(POSTS, POSTS[2])).toEqual([]);
    expect(relatedPosts(POSTS, POSTS[0], 1).map((p) => p.slug)).toEqual(["a"]);
  });

  it("TestAdjacentPosts", () => {
    expect(adjacentPosts(POSTS, "c")).toEqual({ newer: POSTS[0], older: POSTS[2] });
    expect(adjacentPosts(POSTS, "d").newer).toBeUndefined();
    expect(adjacentPosts(POSTS, "a").older).toBeUndefined();
    expect(adjacentPosts(POSTS, "missing")).toEqual({});
  });

  it("TestPagination", () => {
    const many = Array.from({ length: POSTS_PER_PAGE * 2 + 1 }, (_, i) => i);
    expect(pageCount(0)).toBe(1);
    expect(pageCount(many.length)).toBe(3);
    expect(pageOf(many, 3)).toEqual([POSTS_PER_PAGE * 2]);
    expect(pageOf(many, 4)).toEqual([]);
    expect(pageOf(many, 0)).toEqual([]);
  });

  it.each([
    [undefined, 1],
    ["2", 2],
    ["10", 10],
    ["1", null],
    ["0", null],
    ["02", null],
    ["-1", null],
    ["two", null],
  ])("TestParsePageParam_%s", (raw, want) => {
    expect(parsePageParam(raw)).toBe(want);
  });

  it("TestPaths", () => {
    expect(blogPagePath(1)).toBe("/blog");
    expect(blogPagePath(2)).toBe("/blog/page/2");
    expect(tagPath("privacy")).toBe("/blog/tag/privacy");
    expect(tagPath("privacy", 3)).toBe("/blog/tag/privacy/page/3");
    expect(tagLabel("web3-backends")).toBe("Web3 backends");
  });

  it("TestPostBody_unknown_slug_throws", () => {
    expect(() => postBody("no-such-post")).toThrow("no body");
  });
});
