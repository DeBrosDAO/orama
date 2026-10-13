import { describe, expect, it } from "vitest";
import { BlogPostError, comparePosts, isPublished, parsePost, plainText } from "./parse-post";

const DESCRIPTION = "A description long enough for a search result snippet, under the limit.";

function post(frontmatter: string, body = "Some words here.\n\n## First\n\nMore words.\n") {
  return `---\n${frontmatter}\n---\n${body}`;
}

const VALID = `title: A valid post title\ndescription: ${DESCRIPTION}\ndate: 2026-10-09\ntags: [privacy]`;

describe("parsePost", () => {
  it("TestParsePost_happy_path", () => {
    const p = parsePost("a-post", post(VALID, "Intro.\n\n## Why it matters\n\nText.\n\n### A detail\n\nMore.\n"));
    expect(p).toMatchObject({
      slug: "a-post",
      title: "A valid post title",
      description: DESCRIPTION,
      date: "2026-10-09",
      tags: ["privacy"],
      draft: false,
      readingMinutes: 1,
    });
    expect(p.headings).toEqual([
      { depth: 2, text: "Why it matters", id: "why-it-matters" },
      { depth: 3, text: "A detail", id: "a-detail" },
    ]);
  });

  it("TestParsePost_repeated_headings_get_rehype_slug_ids", () => {
    const p = parsePost("a-post", post(VALID, "## Setup\n\nx\n\n## Setup\n\ny\n\n## `code` and **bold**\n"));
    expect(p.headings.map((h) => h.id)).toEqual(["setup", "setup-1", "code-and-bold"]);
  });

  it("TestParsePost_code_blocks_are_not_headings_or_words", () => {
    const body = "One two three.\n\n```bash\n# not a heading\necho a b c d e f\n```\n";
    const p = parsePost("a-post", post(VALID, body));
    expect(p.headings).toEqual([]);
    expect(p.wordCount).toBe(3);
  });

  it("TestParsePost_reading_time_rounds_up", () => {
    const p = parsePost("a-post", post(VALID, `${"word ".repeat(231)}\n`));
    expect(p.readingMinutes).toBe(2);
  });

  it("TestParsePost_cover_and_updated", () => {
    const p = parsePost(
      "a-post",
      post(`${VALID}\nupdated: 2026-10-10\ncover: /images/blog/a-post/cover.png\ncoverAlt: A diagram of the network`),
    );
    expect(p.updated).toBe("2026-10-10");
    expect(p.cover).toEqual({ src: "/images/blog/a-post/cover.png", alt: "A diagram of the network" });
  });

  it.each([
    ["Bad_Slug", VALID, "file name"],
    ["a-post", "title: Short", "title"],
    ["a-post", "title: Long enough title\ndate: 2026-10-09\ntags: [x]", "description"],
    ["a-post", VALID.replace("A valid post title", "x".repeat(70)), "seoTitle"],
    ["a-post", VALID.replace(DESCRIPTION, "Too short."), "description"],
    ["a-post", VALID.replace(DESCRIPTION, "x".repeat(161)), "description"],
    ["a-post", VALID.replace("2026-10-09", "2026-02-30"), "date"],
    ["a-post", VALID.replace("2026-10-09", "October 9"), "date"],
    ["a-post", `${VALID}\nupdated: 2026-10-01`, "before"],
    ["a-post", VALID.replace("[privacy]", "[]"), "tags"],
    ["a-post", VALID.replace("[privacy]", "[Privacy]"), "lowercase"],
    ["a-post", VALID.replace("[privacy]", "[privacy, privacy]"), "twice"],
    ["a-post", `${VALID}\nauthor: me`, "unknown frontmatter key"],
    ["a-post", `${VALID}\ncover: /images/blog/x.png`, "coverAlt"],
    ["a-post", `${VALID}\ncover: /elsewhere/x.png\ncoverAlt: An image`, "/images/blog/"],
    ["a-post", `${VALID}\ndraft: yes please`, "draft"],
  ])("TestParsePost_rejects_%s_%#", (slug, frontmatter, message) => {
    expect(() => parsePost(slug, post(frontmatter))).toThrow(message);
  });

  it("TestParsePost_rejects_missing_frontmatter", () => {
    expect(() => parsePost("a-post", "Just text.")).toThrow(BlogPostError);
  });

  it("TestParsePost_rejects_invalid_yaml", () => {
    expect(() => parsePost("a-post", post("title: [unclosed"))).toThrow("not valid YAML");
  });

  it("TestParsePost_rejects_h1_in_body", () => {
    expect(() => parsePost("a-post", post(VALID, "# A second title\n\nText.\n"))).toThrow("H1");
  });

  it.each(["[x](javascript:alert(1))", "![x](data:image/png;base64,AA)", "[x](relative/page)", "[x]()"])(
    "TestParsePost_rejects_unsafe_link_%s",
    (link) => {
      expect(() => parsePost("a-post", post(VALID, `Text ${link}.\n`))).toThrow("must start with");
    },
  );

  it("TestParsePost_allows_safe_links", () => {
    const body = "[a](https://x.y) [b](/platform) [c](#why) [d](mailto:a@b.c) ![e](/images/blog/a.png)\n";
    expect(parsePost("a-post", post(VALID, body)).wordCount).toBeGreaterThan(0);
  });

  it("TestParsePost_rejects_empty_body", () => {
    expect(() => parsePost("a-post", post(VALID, "\n"))).toThrow("no text");
  });

  it("TestParsePost_long_title_with_seo_title", () => {
    const p = parsePost("a-post", post(`${VALID.replace("A valid post title", "x ".repeat(40).trim())}\nseoTitle: A shorter title`));
    expect(p.seoTitle).toBe("A shorter title");
  });

  it("TestParsePost_windows_line_endings", () => {
    expect(parsePost("a-post", post(VALID).replace(/\n/g, "\r\n")).title).toBe("A valid post title");
  });
});

describe("publishing", () => {
  it("TestIsPublished", () => {
    expect(isPublished({ draft: false, date: "2026-10-09" }, "2026-10-09")).toBe(true);
    expect(isPublished({ draft: false, date: "2026-10-10" }, "2026-10-09")).toBe(false);
    expect(isPublished({ draft: true, date: "2026-01-01" }, "2026-10-09")).toBe(false);
  });

  it("TestComparePosts_newest_first_then_slug", () => {
    const posts = [
      { date: "2026-01-01", slug: "b" },
      { date: "2026-02-01", slug: "z" },
      { date: "2026-01-01", slug: "a" },
    ];
    expect(posts.sort(comparePosts).map((p) => p.slug)).toEqual(["z", "a", "b"]);
  });

  it("TestPlainText", () => {
    expect(plainText("[link](/x) and `code` and **bold** and _em_")).toBe("link and code and bold and em");
    expect(plainText("")).toBe("");
    expect(plainText("The sqlite_master table and ORAMA_ONION_NETWORK")).toBe("The sqlite_master table and ORAMA_ONION_NETWORK");
    expect(plainText("a * b * c and 2*3")).toBe("a * b * c and 2*3");
    expect(plainText("*em* and __strong__")).toBe("em and strong");
  });
});
