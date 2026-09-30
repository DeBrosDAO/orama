import { useEffect } from "react";
import { useSource } from "../data/provider";
import { documentTitle } from "./document-title";

/** Sets the browser tab title while the page is mounted. */
export function useDocumentTitle(title: string | null): void {
  const demo = useSource().origin.kind === "demo";
  useEffect(() => {
    if (title === null) return;
    const previous = document.title;
    document.title = documentTitle(title, demo);
    return () => {
      document.title = previous;
    };
  }, [title, demo]);
}
