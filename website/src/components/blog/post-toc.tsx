import type { BlogHeading } from "../../blog/parse-post";

/** A table of contents only helps once a post has a few sections. */
const MIN_SECTIONS = 3;

/**
 * "On this page": the post's sections as in-page links. Rendered into the
 * HTML, so search engines can offer them as jump links in results.
 */
export function PostToc({ headings }: { headings: BlogHeading[] }) {
  if (headings.filter((h) => h.depth === 2).length < MIN_SECTIONS) return null;
  return (
    <nav aria-label="On this page" className="no-print my-10 border border-dashed border-border p-5 sm:p-6">
      <p className="mb-3 font-mono text-[11px] tracking-[0.2em] uppercase text-muted">On this page</p>
      <ol className="flex flex-col gap-1.5 text-sm">
        {headings.map((h) => (
          <li key={h.id} className={h.depth === 3 ? "pl-4" : undefined}>
            <a href={`#${h.id}`} className="text-muted hover:text-fg transition-colors">
              {h.text}
            </a>
          </li>
        ))}
      </ol>
    </nav>
  );
}
