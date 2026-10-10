import { Suspense, lazy } from "react";
import type { ComponentType, LazyExoticComponent } from "react";
import { useLocation } from "react-router";
import { MDXProvider } from "@mdx-js/react";
import { Page } from "../components/layout/page";
import { Breadcrumbs } from "../components/navigation/breadcrumbs";
import { DocsSidebar } from "../components/navigation/docs-sidebar";
import { TableOfContents } from "../components/navigation/table-of-contents";
import { mdxComponents } from "../components/mdx-components";
import { LoadingSpinner } from "../components/ui/loading-spinner";
import { DOCS_PATH, pageFor } from "../content/pages";
import { normalizePath } from "../content/routes";

/* Vite requires the glob pattern to be a literal string for static analysis.
   Every doc is its own chunk, loaded when its page is shown. */
const modules = import.meta.glob<{ default: ComponentType }>("../docs/**/*.mdx");

const docs = new Map<string, LazyExoticComponent<ComponentType>>();

/** The doc's content as a lazy component, so prerendering waits for it. */
function docComponent(slug: string): LazyExoticComponent<ComponentType> | null {
  const load = modules[`../docs/${slug}.mdx`];
  if (!load) return null;
  let doc = docs.get(slug);
  if (!doc) {
    doc = lazy(load);
    docs.set(slug, doc);
  }
  return doc;
}

function DocNotFound({ slug }: { slug: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-20 gap-4">
      <h1 className="font-mono text-xs tracking-wider uppercase text-muted">Doc not found</h1>
      <p className="text-sm text-muted">
        No documentation found for{" "}
        <code className="font-mono text-accent bg-surface-2 px-1.5 py-0.5 rounded text-sm">{slug}</code>
      </p>
    </div>
  );
}

export default function DocsPage() {
  const path = normalizePath(useLocation().pathname);
  const slug = path.slice(DOCS_PATH.length + 1);
  const meta = pageFor(path);
  const Doc = meta ? docComponent(slug) : null;

  return (
    <Page
      route={{ path, title: meta?.crumbs.at(-1)?.name ?? "Doc not found", description: meta?.description ?? "" }}
      noindex={!Doc}
      breadcrumbs="none"
    >
      <DocsSidebar />
      <TableOfContents />
      <div className="lg:ml-56 min-h-screen">
        <div className="flex justify-center">
          <article className="w-full max-w-3xl px-6 py-8 sm:px-8 sm:py-12">
            {meta && <Breadcrumbs crumbs={meta.crumbs} className="mb-8" />}
            <p className="mb-6 font-mono text-xs text-muted">
              <a href="/llms.txt" className="text-accent hover:underline">
                Agent index (llms.txt)
              </a>
              <span> — fetch this first, then the page it names.</span>
            </p>
            {Doc ? (
              <MDXProvider components={mdxComponents}>
                <Suspense fallback={<div className="flex items-center justify-center py-20"><LoadingSpinner /></div>}>
                  <Doc />
                </Suspense>
              </MDXProvider>
            ) : (
              <DocNotFound slug={slug} />
            )}
          </article>
        </div>
      </div>
    </Page>
  );
}
