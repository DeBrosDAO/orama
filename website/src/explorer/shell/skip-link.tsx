import type { MouseEvent } from "react";

/** The id of the explorer's <main>, which the skip link and every shell that has a main section name. */
export const MAIN_ID = "explorer-main";

/** Classes for a <main> a skip link can send focus to: focusable by script, with no ring (the page is not a control). */
export const MAIN_FOCUS = "focus-visible:outline-none!";

/**
 * The first stop of the keyboard order: jumps over the header, the sections and the network strip to the
 * page. It moves focus itself instead of changing the address, because a hash in the URL would be read by
 * the router as a navigation. Visible only while it has focus.
 */
export function SkipLink() {
  const skip = (event: MouseEvent<HTMLAnchorElement>) => {
    const main = document.getElementById(MAIN_ID);
    if (main === null) return;
    event.preventDefault();
    main.focus();
    main.scrollIntoView();
  };
  return (
    <a
      href={`#${MAIN_ID}`}
      onClick={skip}
      className="sr-only focus-visible:not-sr-only focus-visible:fixed focus-visible:left-3 focus-visible:top-3 focus-visible:z-[80] focus-visible:rounded-lg focus-visible:border focus-visible:border-fg/40 focus-visible:bg-surface-2 focus-visible:px-4 focus-visible:py-2 focus-visible:text-sm focus-visible:text-fg"
    >
      Skip to the page
    </a>
  );
}
