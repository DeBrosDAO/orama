import { useState, useEffect, useRef, useCallback } from "react";
import { useLocation } from "react-router";

export interface HeadingEntry {
  id: string;
  text: string;
  level: number;
}

export function useActiveHeading() {
  const { pathname } = useLocation();
  const [headings, setHeadings] = useState<HeadingEntry[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);
  const observerRef = useRef<IntersectionObserver | null>(null);

  const scan = useCallback(() => {
    const article = document.querySelector("article");
    if (!article) return;

    const elements = article.querySelectorAll<HTMLElement>("h2[id]");
    if (elements.length === 0) return;

    const entries: HeadingEntry[] = [];
    elements.forEach((el) => {
      if (el.id) {
        entries.push({
          id: el.id,
          text: el.textContent?.trim() ?? "",
          level: el.tagName === "H2" ? 2 : 3,
        });
      }
    });

    setHeadings(entries);
    setActiveId(entries[0]?.id ?? null);

    // Clean up previous observer
    observerRef.current?.disconnect();

    const observer = new IntersectionObserver(
      (intersections) => {
        for (const entry of intersections) {
          if (entry.isIntersecting) {
            setActiveId(entry.target.id);
          }
        }
      },
      { rootMargin: "0px 0px -70% 0px" },
    );

    elements.forEach((el) => observer.observe(el));
    observerRef.current = observer;
  }, []);

  useEffect(() => {
    // Reset on route change
    setHeadings([]);
    setActiveId(null);
    observerRef.current?.disconnect();

    // Watch for MDX content to appear in the article
    const article = document.querySelector("article");
    if (!article) return;

    // Try immediately (content may already be rendered)
    scan();

    // Also watch for DOM mutations (lazy-loaded MDX)
    const mutationObs = new MutationObserver(() => {
      const has = article.querySelector("h2[id]");
      if (has) {
        scan();
        mutationObs.disconnect();
      }
    });

    mutationObs.observe(article, { childList: true, subtree: true });

    return () => {
      mutationObs.disconnect();
      observerRef.current?.disconnect();
    };
  }, [pathname, scan]);

  return { headings, activeId };
}
