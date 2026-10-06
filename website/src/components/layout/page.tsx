import { useEffect } from "react";
import { useNavigationType } from "react-router";
import type { ReactNode } from "react";
import { documentTitle } from "../../content/routes";
import type { RouteMeta } from "../../content/routes";
import { pageUrl } from "../../content/seo";

export interface PageProps {
  route: RouteMeta;
  /** Keep this page out of search engines (the unlisted docs, the 404). */
  noindex?: boolean;
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

/**
 * Keeps the document head in step with client-side navigation. The first load
 * of every page already has the right head: the prerenderer writes it.
 */
export function Page({ route, noindex = false, children }: PageProps) {
  const title = documentTitle(route);
  const { description } = route;
  const url = pageUrl(route);
  const navigationType = useNavigationType();

  useEffect(() => {
    document.title = title;
    setAttr('meta[name="description"]', "content", description);
    setAttr('meta[property="og:title"]', "content", title);
    setAttr('meta[property="og:description"]', "content", description);
    setAttr('meta[property="og:url"]', "content", url);
    setAttr('meta[name="twitter:title"]', "content", title);
    setAttr('meta[name="twitter:description"]', "content", description);
    setAttr('link[rel="canonical"]', "href", url);
  }, [title, description, url]);

  useEffect(() => {
    if (!noindex) return;
    const tag = document.createElement("meta");
    tag.name = "robots";
    tag.content = "noindex";
    document.head.appendChild(tag);
    return () => tag.remove();
  }, [noindex]);

  useEffect(() => {
    // POP is the first load and back/forward: the browser already put the
    // reader where they were. Only a new navigation (a clicked link) resets.
    if (navigationType === "POP") return;
    const target = hashTarget();
    if (target) target.scrollIntoView();
    else window.scrollTo(0, 0);
  }, [navigationType]);

  return <>{children}</>;
}
