import { useEffect } from "react";
import { documentTitle } from "./document-title";

/** Sets the browser tab title while the page is mounted. */
export function useDocumentTitle(title: string | null): void {
  useEffect(() => {
    if (title === null) return;
    const previous = document.title;
    document.title = documentTitle(title);
    return () => {
      document.title = previous;
    };
  }, [title]);
}
