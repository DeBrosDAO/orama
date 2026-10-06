import { describe, expect, it } from "vitest";
import { APPS } from "./apps";
import { FACTS } from "./facts";
import { MILESTONES } from "./roadmap";
import { NAV_ROUTES, ROUTE_LIST, documentTitle, normalizePath } from "./routes";
import { SERVICES } from "./services";
import { USE_CASES, USE_CASE_TAG_LABEL } from "./use-cases";

describe("services and use cases", () => {
  it("TestServices_unique_ids", () => {
    expect(new Set(SERVICES.map((s) => s.id)).size).toBe(SERVICES.length);
  });

  it("TestUseCases_reference_real_services", () => {
    const ids = new Set(SERVICES.map((s) => s.id));
    for (const u of USE_CASES) {
      expect(u.uses.length, `${u.id} uses nothing`).toBeGreaterThan(0);
      for (const s of u.uses) expect(ids.has(s), `${u.id} -> unknown service ${s}`).toBe(true);
    }
  });

  it("TestUseCases_unique_and_labelled", () => {
    expect(new Set(USE_CASES.map((u) => u.id)).size).toBe(USE_CASES.length);
    for (const u of USE_CASES) expect(USE_CASE_TAG_LABEL[u.tag]).toBeTruthy();
  });

  it("TestUseCases_only_live_when_an_app_does_it", () => {
    // "Live" claims an app does this on Orama today: only AnChat backs that.
    const live = USE_CASES.filter((u) => u.tag === "live").map((u) => u.id);
    expect(live).toEqual(["messaging"]);
  });
});

describe("routes", () => {
  it("TestRoutes_unique_absolute_paths", () => {
    const paths = ROUTE_LIST.map((r) => r.path);
    expect(new Set(paths).size).toBe(paths.length);
    for (const p of paths) expect(p.startsWith("/")).toBe(true);
  });

  it("TestRoutes_docs_not_in_navigation", () => {
    expect(NAV_ROUTES.some((r) => r.path.startsWith("/docs"))).toBe(false);
    expect(ROUTE_LIST.some((r) => r.path.startsWith("/docs"))).toBe(false);
  });

  it("TestRoutes_descriptions_fit_search_snippets", () => {
    // Search results cut descriptions off past ~160 characters.
    for (const r of ROUTE_LIST) {
      expect(r.description.length, r.path).toBeGreaterThan(50);
      expect(r.description.length, r.path).toBeLessThanOrEqual(160);
    }
  });

  it("TestRoutes_titles_fit_search_results", () => {
    // ...and titles past ~60.
    for (const r of ROUTE_LIST) {
      const t = documentTitle(r);
      expect(t.length, t).toBeLessThanOrEqual(60);
      expect(t.length, t).toBeGreaterThan(15);
    }
    const titles = ROUTE_LIST.map(documentTitle);
    expect(new Set(titles).size).toBe(titles.length);
  });

  it("TestDocumentTitle_home_vs_inner", () => {
    const home = ROUTE_LIST.find((r) => r.path === "/")!;
    expect(documentTitle(home)).toBe(home.title);
    expect(documentTitle({ path: "/x", title: "X", description: "" })).toBe("X · Orama Network");
  });
});

describe("path normalisation", () => {
  it.each([
    ["/", "/"],
    ["/platform", "/platform"],
    ["/platform/", "/platform"],
    ["/platform//", "/platform"],
    ["/docs/x/", "/docs/x"],
    ["//", "/"],
  ])("TestNormalizePath_%s", (input, want) => {
    expect(normalizePath(input)).toBe(want);
  });

  it("TestNormalizePath_self_contained_for_inlining", () => {
    // scripts/prerender.mjs inlines this function's source into every page.
    const inlined = new Function(`return ${normalizePath.toString()}`)() as (p: string) => string;
    expect(inlined("/platform/")).toBe("/platform");
  });
});

describe("proof numbers", () => {
  it("TestFacts_derived_from_data", () => {
    expect(FACTS[0].value).toBe(String(SERVICES.length));
    expect(FACTS[1].value).toBe(String(APPS.length));
  });

  it("TestFacts_commit_count_from_git", () => {
    expect(__REPO_COMMITS__).toBeGreaterThan(0);
    expect(__REPO_FIRST_COMMIT__).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });

  it("TestRoadmap_one_done_step_then_three", () => {
    expect(MILESTONES[0].state).toBe("done");
    expect(MILESTONES.slice(1).map((m) => m.id)).toEqual(["stable", "oramaos", "orama-one"]);
  });
});
