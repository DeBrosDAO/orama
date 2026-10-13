import { Link } from "react-router";
import { ArrowUpRight } from "lucide-react";
import { cn } from "../../lib/utils";
import { postPath, tagLabel } from "../../blog/posts";
import type { BlogPost } from "../../blog/posts";
import { PostMeta } from "./post-meta";

export interface PostCardProps {
  post: BlogPost;
  /** The lead post on the blog's first page: wider, larger title. */
  featured?: boolean;
  /** h2 on index pages; h3 where the card sits under a section heading. */
  headingLevel?: "h2" | "h3";
}

/** A post in a list: tag, title, description, date. The whole card is one link. */
export function PostCard({ post, featured = false, headingLevel = "h2" }: PostCardProps) {
  const Heading = headingLevel;
  return (
    <article
      className={cn(
        "group relative flex flex-col gap-4 border border-dashed border-border p-6 sm:p-8 transition-colors hover:border-fg/30 hover:bg-white/[0.02]",
        featured && "md:col-span-2 lg:col-span-3 sm:p-10",
      )}
    >
      {post.cover && featured && (
        <img
          src={post.cover.src}
          alt={post.cover.alt}
          width={post.cover.width}
          height={post.cover.height}
          className="w-full h-auto border border-dashed border-border"
        />
      )}
      <span className="font-mono text-[11px] tracking-[0.2em] uppercase text-accent">{tagLabel(post.tags[0])}</span>
      <Heading
        className={cn(
          "font-display font-semibold tracking-tight text-fg text-balance",
          featured ? "text-2xl sm:text-4xl" : "text-xl",
        )}
      >
        <Link to={postPath(post.slug)} className="after:absolute after:inset-0 outline-none focus-visible:underline">
          {post.title}
        </Link>
      </Heading>
      <p className={cn("text-muted text-pretty", featured ? "text-base sm:text-lg max-w-3xl" : "text-sm")}>
        {post.description}
      </p>
      <div className="mt-auto flex items-center justify-between gap-4 pt-2">
        <PostMeta post={post} />
        <ArrowUpRight
          size={16}
          aria-hidden="true"
          className="shrink-0 text-muted transition-transform group-hover:translate-x-0.5 group-hover:-translate-y-0.5 group-hover:text-fg"
        />
      </div>
    </article>
  );
}
