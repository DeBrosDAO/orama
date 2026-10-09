import { cn } from "../../lib/utils";
import type { BlogPost } from "../../blog/posts";

const DATE_FORMAT = new Intl.DateTimeFormat("en-US", {
  year: "numeric",
  month: "long",
  day: "numeric",
  // Fixed zone: the prerendered date and the hydrated one must agree.
  timeZone: "UTC",
});

/** "October 9, 2026" for a YYYY-MM-DD day. */
export const formatDay = (day: string) => DATE_FORMAT.format(new Date(`${day}T00:00:00Z`));

export interface PostMetaProps {
  post: BlogPost;
  /** Also show the update date and the author (the post page itself). */
  full?: boolean;
  className?: string;
}

/** Date and reading time; on the post itself, also the update date and author. */
export function PostMeta({ post, full = false, className }: PostMetaProps) {
  const parts = [
    <time key="date" dateTime={post.date}>
      {formatDay(post.date)}
    </time>,
  ];
  if (full && post.updated && post.updated !== post.date) {
    parts.push(
      <span key="updated">
        Updated <time dateTime={post.updated}>{formatDay(post.updated)}</time>
      </span>,
    );
  }
  parts.push(<span key="read">{post.readingMinutes} min read</span>);
  if (full) parts.unshift(<span key="by">By Orama Network</span>);

  return (
    <p className={cn("flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11px] tracking-wider uppercase text-muted", className)}>
      {parts.map((part, i) => (
        <span key={i} className="inline-flex items-center gap-2">
          {i > 0 && <span aria-hidden="true" className="text-muted/40">/</span>}
          {part}
        </span>
      ))}
    </p>
  );
}
