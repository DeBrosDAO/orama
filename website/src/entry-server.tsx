import { StrictMode } from "react";
import { prerender } from "react-dom/static";
import { StaticRouter } from "react-router";
import { App } from "./app";
import { ROUTE_LIST, documentTitle, normalizePath } from "./content/routes";
import type { RouteMeta } from "./content/routes";
import { INVESTOR_PDF, SITE_URL } from "./content/site";
import { structuredData } from "./content/seo";

export { INVESTOR_PDF, ROUTE_LIST, SITE_URL, documentTitle, normalizePath, structuredData };
export type { RouteMeta };

/**
 * Render one page to HTML at build time. prerender (unlike renderToString)
 * waits for every lazy page and Suspense boundary to resolve, so the output
 * is the finished page, not a loading spinner.
 *
 * progressiveChunkSize: by default React 19 streams any Suspense boundary
 * larger than ~12 KB "out of order": the fallback spinner goes in place, the
 * real page into a hidden <div>, and a script swaps them after the next frame.
 * For a static page that means crawlers and first paint see a spinner. An
 * infinite chunk size keeps every boundary inline.
 */
export async function render(url: string): Promise<string> {
  const { prelude } = await prerender(
    <StrictMode>
      <StaticRouter location={url}>
        <App />
      </StaticRouter>
    </StrictMode>,
    { progressiveChunkSize: Number.POSITIVE_INFINITY },
  );
  return new Response(prelude).text();
}
