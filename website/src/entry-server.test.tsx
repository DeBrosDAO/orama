import { describe, expect, it } from "vitest";
import { NOT_FOUND_PROBE, PAGES, POSTS, render, structuredData } from "./entry-server";
import { postPath } from "./blog/posts";
import { EXPLORER_PATH } from "./content/pages";
import { ROUTES } from "./content/routes";
import { WHITEPAPER, WHITEPAPER_SHORT } from "./content/whitepaper";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * Renders every prerendered page exactly as the build does. Catches a page
 * that falls through to the 404, a page without exactly one heading, and
 * React streaming a page in behind a spinner (what crawlers and first paint
 * would see).
 */
describe("prerendered pages", () => {
  it.each(PAGES.map((p) => [p.path, p] as const))("TestRender_%s", async (_path, page) => {
    const html = await render(page.path);
    expect(html.match(/<h1[\s>]/g)).toHaveLength(1);
    expect(html).not.toContain("This page doesn&#x27;t exist.");
    expect(html).not.toContain("<!--$?-->");
    expect(html).not.toContain('<template id="B:');
  }, 30_000);

  // The explorer keeps its own layout; its trail is only in the structured data.
  it.each(PAGES.filter((p) => p.crumbs.length > 1 && p.path !== EXPLORER_PATH).map((p) => [p.path, p] as const))(
    "TestRender_breadcrumbs_%s",
    async (_path, page) => {
      const html = await render(page.path);
      expect(html).toContain('aria-label="Breadcrumb"');
      expect(html).toContain(`aria-current="page"`);
    },
    30_000,
  );

  it("TestRender_whitepaper_download_section", async () => {
    // React separates adjacent text nodes with <!-- -->; the reader sees none.
    const html = (await render(ROUTES.whitepaper.path)).replaceAll("<!-- -->", "");
    const { files, version, referenceTotal } = WHITEPAPER;
    expect(files).toHaveLength(4);
    // The primary download comes first, then the three reference volumes.
    const hrefs = files.map((f) => html.indexOf(`href="${f.href}"`));
    expect(hrefs.every((i) => i > 0)).toBe(true);
    expect(hrefs).toEqual([...hrefs].sort((a, b) => a - b));
    for (const f of files) expect(html).toContain(`PDF · ${f.pages} pages · ${f.size} · v${version}`);
    expect(html).toContain(`${referenceTotal.pages.toLocaleString("en-US")} pages in three volumes`);
    expect(html).toContain("docs/whitepaper/");
    expect(html).toContain(`at version ${version}`);
    // The short overview is still rendered below the downloads.
    expect(html.indexOf("Download the whitepaper")).toBeLessThan(html.indexOf("<h1"));
    expect(html).toContain("The problem");
  }, 30_000);

  it("TestWhitepaper_manifest_matches_version_file", () => {
    expect(WHITEPAPER.version).toBe(readFileSync(resolve(__dirname, "../../VERSION"), "utf-8").trim());
    for (const f of WHITEPAPER.files) {
      expect(f.href).toContain(`-v${WHITEPAPER.version}`);
      expect(f.pages).toBeGreaterThan(0);
    }
    expect(WHITEPAPER_SHORT.pages).toBeLessThan(WHITEPAPER.referenceTotal.pages);
  });

  it("TestRender_unknown_path_is_404", async () => {
    expect(await render("/no-such-page")).toContain("This page doesn&#x27;t exist.");
    expect(await render(NOT_FOUND_PROBE)).toContain("This page doesn&#x27;t exist.");
  });

  it.each(["/blog/no-such-post", "/blog/page/1", "/blog/page/999", "/blog/tag/no-such-tag", "/docs/developer/no-such-doc"])(
    "TestRender_missing_blog_or_doc_%s",
    async (path) => {
      const html = await render(path);
      expect(html).toMatch(/This page doesn&#x27;t exist\.|Doc not found/);
    },
  );

  it("TestRender_post_body_is_in_the_html", async () => {
    const post = POSTS[0];
    expect(post, "at least one published post").toBeDefined();
    const html = await render(postPath(post.slug));
    // The Markdown body, not a spinner: every section heading is there with its id.
    for (const h of post.headings) expect(html).toContain(`id="${h.id}"`);
    expect(html).toContain(`dateTime="${post.date}"`);
  }, 30_000);

  it.each(PAGES.map((p) => [p.path, p] as const))("TestStructuredData_%s", (_path, page) => {
    const json = structuredData(page, "2026-01-01");
    expect(json).not.toContain("<");
    const graph = (JSON.parse(json) as { "@graph": { "@type": string; url?: string }[] })["@graph"];
    expect(graph.find((n) => n["@type"].endsWith("Page") && n.url?.startsWith("https://orama.network"))).toBeDefined();
    if (page.path !== "/") expect(graph.some((n) => n["@type"] === "BreadcrumbList")).toBe(true);
  });
});
