import { afterEach, describe, expect, it, vi } from "vitest";
import { pageFor } from "../../content/pages";
import { canonicalPath, headDeps, syncHead } from "./page";

describe("canonicalPath", () => {
  it("TestCanonicalPath_an_indexable_page_names_its_own_page_path", () => {
    expect(canonicalPath("/explorer", false, "/explorer")).toBe("/explorer");
    expect(canonicalPath("/explorer", false, "/explorer/")).toBe("/explorer");
  });

  it("TestCanonicalPath_a_page_kept_out_of_search_names_the_address_it_was_opened_at", () => {
    // The explorer page component renders every /explorer/... address with the landing's metadata.
    expect(canonicalPath("/explorer", true, "/explorer/tx/ab12")).toBe("/explorer/tx/ab12");
    expect(canonicalPath("/explorer", true, "/explorer/custom/")).toBe("/explorer/custom");
  });

  it("TestCanonicalPath_the_root_stays_the_root", () => {
    expect(canonicalPath("/", true, "/")).toBe("/");
  });
});

const page = pageFor("/explorer");

describe("headDeps", () => {
  it("TestHeadDeps_navigating_inside_one_page_component_changes_what_the_head_is_written_from", () => {
    if (!page) throw new Error("the explorer page is missing from the page list");
    expect(headDeps(page, true, "/explorer/tx/a")).not.toEqual(headDeps(page, true, "/explorer/block/9"));
  });

  it("TestHeadDeps_a_trailing_slash_is_not_a_navigation", () => {
    if (!page) throw new Error("the explorer page is missing from the page list");
    expect(headDeps(page, true, "/explorer/tx/a")).toEqual(headDeps(page, true, "/explorer/tx/a/"));
  });
});

/** A document with just the head tags syncHead writes. */
function fakeDocument() {
  const tags = new Map<string, Record<string, string>>();
  const tag = (selector: string) => {
    let el = tags.get(selector);
    if (!el) tags.set(selector, (el = {}));
    return { setAttribute: (name: string, value: string) => void (el![name] = value), textContent: "" };
  };
  return {
    tags,
    doc: {
      title: "",
      head: { querySelector: tag, querySelectorAll: () => ({ forEach: () => undefined }), append: () => undefined },
      getElementById: () => null,
      createElement: () => ({ innerHTML: "", content: "" }),
    },
  };
}

describe("syncHead", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("TestSyncHead_navigating_between_noindex_addresses_rewrites_the_canonical_and_og_url", () => {
    if (!page) throw new Error("the explorer page is missing from the page list");
    const { doc, tags } = fakeDocument();
    vi.stubGlobal("document", doc);
    const at = (pathname: string) => vi.stubGlobal("window", { location: { pathname } });
    at("/explorer/tx/ab12");
    syncHead(page, true);
    expect(tags.get('link[rel="canonical"]')?.href).toBe("https://orama.network/explorer/tx/ab12");
    expect(tags.get('meta[property="og:url"]')?.content).toBe("https://orama.network/explorer/tx/ab12");
    expect(tags.get('meta[name="robots"]')?.content).toBe("noindex, follow");
    at("/explorer/block/9");
    syncHead(page, true);
    expect(tags.get('link[rel="canonical"]')?.href).toBe("https://orama.network/explorer/block/9");
    expect(tags.get('meta[property="og:url"]')?.content).toBe("https://orama.network/explorer/block/9");
  });

  it("TestSyncHead_the_indexable_landing_names_itself_after_a_deep_link", () => {
    if (!page) throw new Error("the explorer page is missing from the page list");
    const { doc, tags } = fakeDocument();
    vi.stubGlobal("document", doc);
    vi.stubGlobal("window", { location: { pathname: "/explorer" } });
    syncHead(page, false);
    expect(tags.get('link[rel="canonical"]')?.href).toBe("https://orama.network/explorer");
    expect(tags.get('meta[name="robots"]')?.content).toContain("index, follow");
  });
});
