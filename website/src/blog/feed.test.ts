import { describe, expect, it } from "vitest";
import { FEED_LIMIT, rfc822, rssFeed } from "./feed";
import type { BlogPost } from "./posts";

const make = (slug: string, date: string, extra: Partial<BlogPost> = {}): BlogPost => ({
  slug,
  title: `Title <${slug}> & co`,
  description: "Desc",
  date,
  tags: ["privacy"],
  draft: false,
  wordCount: 1,
  readingMinutes: 1,
  headings: [],
  ...extra,
});

describe("rssFeed", () => {
  it("TestRssFeed_items_escaped_and_dated", () => {
    const xml = rssFeed([make("b", "2026-10-09", { updated: "2026-10-12" }), make("a", "2026-01-02")]);
    expect(xml).toContain("<title>Title &lt;b&gt; &amp; co</title>");
    expect(xml).toContain("<link>https://orama.network/blog/b</link>");
    expect(xml).toContain('<guid isPermaLink="true">https://orama.network/blog/a</guid>');
    expect(xml).toContain(`<pubDate>${rfc822("2026-01-02")}</pubDate>`);
    expect(xml).toContain(`<lastBuildDate>${rfc822("2026-10-12")}</lastBuildDate>`);
    expect(xml).toContain("<category>privacy</category>");
    expect(xml.indexOf("/blog/b<")).toBeLessThan(xml.indexOf("/blog/a<"));
  });

  it("TestRssFeed_empty", () => {
    const xml = rssFeed([]);
    expect(xml).toContain("<channel>");
    expect(xml).not.toContain("<item>");
    expect(xml).not.toContain("lastBuildDate");
  });

  it("TestRssFeed_limited", () => {
    const posts = Array.from({ length: FEED_LIMIT + 5 }, (_, i) => make(`p${i}`, "2026-01-01"));
    expect(rssFeed(posts).match(/<item>/g)).toHaveLength(FEED_LIMIT);
  });

  it("TestRfc822", () => {
    expect(rfc822("2026-10-09")).toBe("Fri, 09 Oct 2026 00:00:00 GMT");
  });
});
