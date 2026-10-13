import { describe, expect, it } from "vitest";
import { DOC_META } from "virtual:docs-meta";
import { ALL_DOCS } from "../data/docs-navigation";
import { POSTS, postPath } from "../blog/posts";
import { MIN_POSTS_TO_INDEX_TAG, PAGES, fitTitle, knownLastModified, pageFor } from "./pages";
import { allTags, tagPath } from "../blog/posts";
import { NAV, isActiveEntry, isActivePath, isGroup } from "./navigation";
import { ROBOTS_INDEX, extraHeadTags, structuredData } from "./seo";
import { sitemapIndexXml, sitemapXml } from "./sitemap";

type Node = Record<string, unknown> & { "@type": string };
const graphOf = (json: string) => (JSON.parse(json) as { "@graph": Node[] })["@graph"];

describe("pages", () => {
  it("TestPages_unique_paths_and_titles", () => {
    const paths = PAGES.map((p) => p.path);
    expect(new Set(paths).size).toBe(paths.length);
    const titles = PAGES.map((p) => p.title);
    expect(new Set(titles).size, "two pages share a title").toBe(titles.length);
  });

  it("TestPages_fit_search_results", () => {
    for (const p of PAGES) {
      expect(p.title.length, p.title).toBeLessThanOrEqual(60);
      expect(p.description.length, p.path).toBeGreaterThanOrEqual(40);
      expect(p.description.length, p.path).toBeLessThanOrEqual(160);
    }
  });

  it("TestPages_crumbs_start_home_end_self", () => {
    for (const p of PAGES) {
      expect(p.crumbs[0], p.path).toEqual({ name: "Home", path: "/" });
      expect(p.crumbs.at(-1)?.path, p.path).toBe(p.path);
      expect(new Set(p.crumbs.map((c) => c.path)).size, p.path).toBe(p.crumbs.length);
      for (const c of p.crumbs) expect(pageFor(c.path), `${p.path} -> ${c.path}`).toBeDefined();
    }
  });

  it("TestPages_every_doc_file_is_listed_and_every_listing_has_a_file", () => {
    const listed = new Set(ALL_DOCS.map((d) => d.link.slug));
    expect([...Object.keys(DOC_META)].filter((s) => !listed.has(s)), "docs missing from the navigation").toEqual([]);
    for (const slug of listed) expect(pageFor(`/docs/${slug}`), slug).toBeDefined();
  });

  it("TestPages_posts_have_article_meta", () => {
    for (const post of POSTS) {
      const page = pageFor(postPath(post.slug))!;
      expect(page.schema).toBe("BlogPosting");
      expect(page.image.path).toBe(`/og/blog/${post.slug}.png`);
      expect(knownLastModified(page)).toBe(post.updated ?? post.date);
    }
  });

  it("TestPages_thin_tag_pages_noindexed_and_out_of_sitemap", () => {
    for (const { tag, count } of allTags(POSTS)) {
      const page = pageFor(tagPath(tag))!;
      const thin = count < MIN_POSTS_TO_INDEX_TAG;
      expect(page.noindex, tag).toBe(thin);
      expect(page.sitemap, tag).toBe(thin ? null : "blog");
    }
    for (const p of PAGES.filter((p) => !p.path.startsWith("/blog/tag/"))) expect(p.noindex, p.path).toBeFalsy();
  });

  it("TestPageFor_normalises_and_misses", () => {
    expect(pageFor("/platform/")?.path).toBe("/platform");
    expect(pageFor("/no-such-page")).toBeUndefined();
  });

  it("TestFitTitle", () => {
    expect(fitTitle("Cache", "Orama Docs")).toBe("Cache · Orama Docs");
    expect(fitTitle("x".repeat(55), "Orama Docs")).toBe("x".repeat(55));
  });
});

describe("structured data", () => {
  it("TestStructuredData_breadcrumbs_absolute", () => {
    const page = pageFor("/docs/developer/cache")!;
    const crumbs = graphOf(structuredData(page)).find((n) => n["@type"] === "BreadcrumbList")!;
    const items = crumbs.itemListElement as { position: number; item: string; name: string }[];
    expect(items.map((i) => i.position)).toEqual(items.map((_, i) => i + 1));
    expect(items[0].item).toBe("https://orama.network/");
    expect(items.at(-1)?.item).toBe("https://orama.network/docs/developer/cache");
  });

  it("TestStructuredData_home_has_no_breadcrumbs", () => {
    expect(graphOf(structuredData(pageFor("/")!)).some((n) => n["@type"] === "BreadcrumbList")).toBe(false);
  });

  it("TestStructuredData_blog_posting", () => {
    const post = POSTS[0];
    const article = graphOf(structuredData(pageFor(postPath(post.slug))!)).find((n) => n["@type"] === "BlogPosting")!;
    expect(article).toMatchObject({
      headline: post.title,
      datePublished: post.date,
      dateModified: post.updated ?? post.date,
      keywords: post.tags,
      url: `https://orama.network/blog/${post.slug}`,
    });
  });

  it("TestStructuredData_doc_uses_lastmod", () => {
    const doc = graphOf(structuredData(pageFor("/docs/developer/cache")!, "2026-05-05")).find((n) => n["@type"] === "TechArticle")!;
    expect(doc.dateModified).toBe("2026-05-05");
  });

  it("TestExtraHeadTags", () => {
    const post = POSTS[0];
    const tags = extraHeadTags(pageFor(postPath(post.slug))!);
    expect(tags).toContain(`article:published_time" content="${post.date}"`);
    expect(tags).toContain('type="application/rss+xml"');
    expect(extraHeadTags(pageFor("/platform")!)).toBe("");
    expect(ROBOTS_INDEX).toContain("max-image-preview:large");
  });
});

describe("sitemaps", () => {
  it("TestSitemapXml", () => {
    const xml = sitemapXml([pageFor("/")!, pageFor("/platform")!], () => "2026-10-09");
    expect(xml).toContain("<loc>https://orama.network/</loc>");
    expect(xml).toContain("<loc>https://orama.network/platform</loc>");
    expect(xml.match(/<lastmod>2026-10-09<\/lastmod>/g)).toHaveLength(2);
    expect(xml).not.toContain("priority");
  });

  it("TestSitemapIndexXml", () => {
    const xml = sitemapIndexXml([{ group: "docs", lastmod: "2026-10-01" }]);
    expect(xml).toContain("<loc>https://orama.network/sitemap-docs.xml</loc>");
    expect(xml).toContain("<sitemapindex");
  });
});

describe("navigation", () => {
  it("TestNav_every_link_is_a_page", () => {
    for (const entry of NAV) {
      for (const item of isGroup(entry) ? entry.items : [entry]) {
        expect(pageFor(item.path), item.path).toBeDefined();
      }
    }
  });

  it("TestIsActivePath", () => {
    expect(isActivePath("/blog/a-post", "/blog")).toBe(true);
    expect(isActivePath("/blogger", "/blog")).toBe(false);
    expect(isActivePath("/platform", "/")).toBe(false);
    expect(isActiveEntry("/docs/developer/cache", NAV.find((e) => e.label === "Information")!)).toBe(true);
  });
});
