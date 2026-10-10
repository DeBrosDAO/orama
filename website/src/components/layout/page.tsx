import { useEffect, useMemo } from "react";
import { useNavigationType } from "react-router";
import type { ReactNode } from "react";
import { documentTitle } from "../../content/routes";
import type { RouteMeta } from "../../content/routes";
import { DEFAULT_IMAGE, pageFor } from "../../content/pages";
import type { PageMeta } from "../../content/pages";
import { ROBOTS_INDEX, ROBOTS_NOINDEX, absoluteUrl, extraHeadTags, structuredData } from "../../content/seo";
import { Breadcrumbs } from "../navigation/breadcrumbs";

export interface PageProps {
  route: RouteMeta;
  /** Keep this page out of search engines (the 404, an unknown doc). */
  noindex?: boolean;
  /** Where the breadcrumb trail goes: above the page, or nowhere (the page places it itself). */
  breadcrumbs?: "top" | "none";
  children: ReactNode;
}

function setAttr(selector: string, attr: "content" | "href", value: string) {
  document.head.querySelector(selector)?.setAttribute(attr, value);
}

/** The in-page anchor to jump to, if the hash names an element on the page. */
function hashTarget(): HTMLElement | null {
  const raw = window.location.hash.slice(1);
  if (!raw) return null;
  let id = raw;
  try {
    id = decodeURIComponent(raw);
  } catch {
    // A malformed escape in a hand-typed URL: look the id up as written.
  }
  return document.getElementById(id);
}

/** A page outside the prerendered set (the 404, an unknown doc): its own head, no trail. */
function looseMeta(route: RouteMeta): PageMeta {
  return {
    path: route.path,
    title: documentTitle(route),
    description: route.description,
    schema: "WebPage",
    crumbs: [],
    image: DEFAULT_IMAGE,
    chunks: [],
    sitemap: "pages",
  };
}

/** Replace the per-kind tags (article dates, the feed link) with this page's. */
function syncExtraTags(page: PageMeta) {
  document.head
    .querySelectorAll('meta[property^="article:"], link[rel="alternate"][type="application/rss+xml"]')
    .forEach((el) => el.remove());
  const template = document.createElement("template");
  template.innerHTML = extraHeadTags(page);
  document.head.append(template.content);
}

/**
 * True while the document still shows the prerendered page it was loaded
 * with: its JSON-LD carries the sitemap date, which the browser cannot know,
 * so it is left as written.
 */
let showingPrerendered = true;

function isPrerenderedPage(path: string): boolean {
  const wasPrerendered = showingPrerendered && document.getElementById("root")?.dataset.prerendered === path;
  showingPrerendered = false;
  return wasPrerendered;
}

function syncHead(page: PageMeta, noindex: boolean) {
  const url = absoluteUrl(page.path);
  const image = absoluteUrl(page.image.path);
  document.title = page.title;
  setAttr('meta[name="description"]', "content", page.description);
  setAttr('meta[name="robots"]', "content", noindex || page.noindex ? ROBOTS_NOINDEX : ROBOTS_INDEX);
  setAttr('meta[property="og:type"]', "content", page.post ? "article" : "website");
  setAttr('meta[property="og:title"]', "content", page.title);
  setAttr('meta[property="og:description"]', "content", page.description);
  setAttr('meta[property="og:url"]', "content", url);
  setAttr('meta[property="og:image"]', "content", image);
  setAttr('meta[property="og:image:alt"]', "content", page.image.alt);
  setAttr('meta[name="twitter:title"]', "content", page.title);
  setAttr('meta[name="twitter:description"]', "content", page.description);
  setAttr('meta[name="twitter:image"]', "content", image);
  setAttr('meta[name="twitter:image:alt"]', "content", page.image.alt);
  setAttr('link[rel="canonical"]', "href", url);
  const jsonLd = document.head.querySelector('script[type="application/ld+json"]');
  if (jsonLd && !isPrerenderedPage(page.path)) jsonLd.textContent = structuredData(page);
  syncExtraTags(page);
}

/**
 * Keeps the document head in step with client-side navigation and shows the
 * page's breadcrumb trail. The first load of every page already has the right
 * head: the prerenderer writes it from the same page metadata.
 */
export function Page({ route, noindex = false, breadcrumbs = "top", children }: PageProps) {
  const page = useMemo(() => pageFor(route.path) ?? looseMeta(route), [route]);
  const navigationType = useNavigationType();

  useEffect(() => {
    syncHead(page, noindex);
  }, [page, noindex]);

  useEffect(() => {
    // POP is the first load and back/forward: the browser already put the
    // reader where they were. Only a new navigation (a clicked link) resets.
    if (navigationType === "POP") return;
    const target = hashTarget();
    if (target) target.scrollIntoView();
    else window.scrollTo(0, 0);
  }, [navigationType]);

  return (
    <>
      {breadcrumbs === "top" && (
        <Breadcrumbs crumbs={page.crumbs} className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 pt-8" />
      )}
      {children}
    </>
  );
}
