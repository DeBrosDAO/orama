import { MDXProvider } from "@mdx-js/react";
import { Printer } from "lucide-react";
import { Page } from "../components/layout/page";
import { mdxBaseComponents } from "../components/mdx-base";
import { ROUTES } from "../content/routes";
import Whitepaper from "../../../docs/whitepaper/WHITEPAPER.md";

/**
 * The whitepaper's source of truth is docs/whitepaper/WHITEPAPER.md in the
 * repository; this page renders that file, so the site can never serve a copy
 * that disagrees with the repo.
 */
export default function WhitepaperPage() {
  return (
    <Page route={ROUTES.whitepaper}>
      <div className="flex justify-center">
        <article className="print-light w-full max-w-3xl px-6 py-12 sm:px-8 sm:py-16">
          <div className="no-print flex justify-end mb-6">
            <button
              type="button"
              onClick={() => window.print()}
              className="inline-flex items-center gap-2 px-3 py-1.5 border border-border/70 font-mono text-[11px] tracking-wider uppercase text-muted hover:text-fg hover:border-fg/30 transition-colors cursor-pointer"
            >
              <Printer size={12} />
              Print / save as PDF
            </button>
          </div>
          <MDXProvider components={mdxBaseComponents}>
            <Whitepaper />
          </MDXProvider>
        </article>
      </div>
    </Page>
  );
}
