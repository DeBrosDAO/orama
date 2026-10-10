import { Link } from "react-router";
import { cn } from "../../lib/utils";
import { tagLabel, tagPath } from "../../blog/posts";

export interface TagListProps {
  tags: string[];
  /** Highlight this tag (the tag page being shown). */
  current?: string;
  /** Post counts per tag, shown after the label. */
  counts?: Map<string, number>;
  className?: string;
}

/** Tags as links to their tag pages. */
export function TagList({ tags, current, counts, className }: TagListProps) {
  return (
    <ul className={cn("flex flex-wrap gap-2", className)}>
      {tags.map((tag) => (
        <li key={tag}>
          <Link
            to={tagPath(tag)}
            aria-current={tag === current ? "page" : undefined}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-full border px-3 py-1 font-mono text-[11px] tracking-wider uppercase transition-colors",
              tag === current
                ? "border-accent/70 text-fg bg-white/[0.05]"
                : "border-border/60 text-muted hover:text-fg hover:border-fg/30",
            )}
          >
            {tagLabel(tag)}
            {counts && <span className="text-muted/60">{counts.get(tag)}</span>}
          </Link>
        </li>
      ))}
    </ul>
  );
}
