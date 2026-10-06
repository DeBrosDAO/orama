import { describe, expect, it } from "vitest";
import { ROUTE_LIST, render, structuredData, documentTitle } from "./entry-server";

/**
 * Renders every public page exactly as the build does. Catches a page that
 * falls through to the 404, a page without a heading, and React streaming a
 * page in behind a spinner (what crawlers and first paint would see).
 */
describe("prerendered pages", () => {
  it.each(ROUTE_LIST.map((r) => [r.path, r] as const))("TestRender_%s", async (_path, route) => {
    const html = await render(route.path);
    expect(html).toMatch(/<h1[\s>]/);
    expect(html.match(/<h1[\s>]/g)).toHaveLength(1);
    expect(html).not.toContain("This page doesn&#x27;t exist.");
    expect(html).not.toContain("<!--$?-->");
    expect(html).not.toContain('<template id="B:');
  }, 20_000);

  it("TestRender_unknown_path_is_404", async () => {
    const html = await render("/no-such-page");
    expect(html).toContain("This page doesn&#x27;t exist.");
  });

  it.each(ROUTE_LIST.map((r) => [r.path, r] as const))("TestStructuredData_%s", (_path, route) => {
    const json = structuredData(route, documentTitle(route));
    expect(json).not.toContain("<");
    const data = JSON.parse(json) as { "@graph": { "@type": string; url?: string }[] };
    const page = data["@graph"].find((n) => n.url?.startsWith("https://orama.network"));
    expect(page).toBeDefined();
  });
});
