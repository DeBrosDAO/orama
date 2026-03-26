import { useState, useEffect, type ComponentType } from "react";
import { useLocation } from "react-router";
import { Page } from "../components/layout/page";
import { DocsSidebar } from "../components/navigation/docs-sidebar";
import { TableOfContents } from "../components/navigation/table-of-contents";
import { mdxComponents } from "../components/mdx-components";
import { MDXProvider } from "@mdx-js/react";
import { LoadingSpinner } from "../components/ui/loading-spinner";
import { DOCS_SECTIONS } from "../data/docs-navigation";

const DEFAULT_SLUG = "developer/getting-started";

/** All known doc slugs for resolving titles */
const SLUG_TITLE_MAP = new Map(
  DOCS_SECTIONS.flatMap((section) =>
    section.links.map((link) => [link.slug, link.title]),
  ),
);

/* Vite requires the glob pattern to be a literal string for static analysis.
   We pre-resolve all MDX modules and select the matching one at runtime. */
const modules = import.meta.glob("../docs/**/*.mdx");

function getDocModule(slug: string) {
  const key = `../docs/${slug}.mdx`;
  return modules[key];
}

function DocNotFound({ slug }: { slug: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-20 gap-4">
      <p className="font-mono text-xs tracking-wider uppercase text-muted">
        Doc not found
      </p>
      <p className="text-sm text-muted">
        No documentation found for{" "}
        <code className="font-mono text-accent bg-surface-2 px-1.5 py-0.5 rounded text-sm">
          {slug}
        </code>
      </p>
    </div>
  );
}

export default function DocsPage() {
  const { pathname } = useLocation();

  // Extract slug from /docs/... path, fall back to default
  const effectiveSlug = pathname.replace(/^\/docs\/?/, "") || DEFAULT_SLUG;
  const pageTitle = SLUG_TITLE_MAP.get(effectiveSlug) ?? "Documentation";

  const [Content, setContent] = useState<ComponentType | null>(null);
  const [loading, setLoading] = useState(true);
  const [notFound, setNotFound] = useState(false);

  useEffect(() => {
    setLoading(true);
    setNotFound(false);

    const loader = getDocModule(effectiveSlug);
    if (!loader) {
      setContent(null);
      setNotFound(true);
      setLoading(false);
      return;
    }

    loader().then((mod) => {
      setContent(() => (mod as { default: ComponentType }).default);
      setLoading(false);
    });
  }, [effectiveSlug]);

  return (
    <Page title={`${pageTitle} — Docs`}>
      <DocsSidebar />
      <TableOfContents />
      <div className="lg:ml-56 min-h-screen">
        <div className="flex justify-center">
          <article className="w-full max-w-3xl px-6 py-8 sm:px-8 sm:py-12">
            <MDXProvider components={mdxComponents}>
              {loading ? (
                <div className="flex items-center justify-center py-20">
                  <LoadingSpinner />
                </div>
              ) : Content ? (
                <Content />
              ) : notFound ? (
                <DocNotFound slug={effectiveSlug} />
              ) : null}
            </MDXProvider>
          </article>
        </div>
      </div>
    </Page>
  );
}
