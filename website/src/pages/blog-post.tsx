import { Suspense } from "react";
import { Link, useParams } from "react-router";
import { MDXProvider } from "@mdx-js/react";
import { ArrowLeft, ArrowRight } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { Breadcrumbs } from "../components/navigation/breadcrumbs";
import { PostCard } from "../components/blog/post-card";
import { PostMeta } from "../components/blog/post-meta";
import { PostToc } from "../components/blog/post-toc";
import { TagList } from "../components/blog/tag-list";
import { LoadingSpinner } from "../components/ui/loading-spinner";
import { mdxComponents } from "../components/mdx-components";
import { pageFor } from "../content/pages";
import { absoluteUrl } from "../content/seo";
import { BLOG_PATH, POSTS, adjacentPosts, findPost, postBody, postPath, relatedPosts } from "../blog/posts";
import type { BlogPost as Post } from "../blog/posts";
import NotFound from "./not-found";

function shareOnX(post: Post): string {
  const params = new URLSearchParams({ text: post.title, url: absoluteUrl(postPath(post.slug)) });
  return `https://x.com/intent/post?${params}`;
}

function PostNeighbours({ post }: { post: Post }) {
  const { newer, older } = adjacentPosts(POSTS, post.slug);
  if (!newer && !older) return null;
  const cell = "flex flex-col gap-1 border border-dashed border-border p-5 hover:border-fg/30 transition-colors";
  return (
    <nav aria-label="More articles" className="grid grid-cols-1 sm:grid-cols-2 gap-4 mt-12">
      {older ? (
        <Link to={postPath(older.slug)} className={cell}>
          <span className="inline-flex items-center gap-1.5 font-mono text-[11px] tracking-wider uppercase text-muted">
            <ArrowLeft size={11} aria-hidden="true" /> Previous
          </span>
          <span className="text-fg font-medium">{older.title}</span>
        </Link>
      ) : (
        <span />
      )}
      {newer && (
        <Link to={postPath(newer.slug)} className={`${cell} sm:text-right sm:items-end`}>
          <span className="inline-flex items-center gap-1.5 font-mono text-[11px] tracking-wider uppercase text-muted">
            Next <ArrowRight size={11} aria-hidden="true" />
          </span>
          <span className="text-fg font-medium">{newer.title}</span>
        </Link>
      )}
    </nav>
  );
}

export default function BlogPost() {
  const post = findPost(useParams().slug ?? "");
  if (!post) return <NotFound />;
  const path = postPath(post.slug);
  const Body = postBody(post.slug);
  const related = relatedPosts(POSTS, post);

  return (
    <Page route={{ path, title: post.title, description: post.description }} breadcrumbs="none">
      <div className="flex justify-center">
        <article className="w-full max-w-3xl px-6 pt-8 pb-12 sm:px-8 sm:pb-16">
          <Breadcrumbs crumbs={pageFor(path)?.crumbs ?? []} className="mb-10" />
          <header className="flex flex-col gap-5 pb-8 border-b border-dashed border-border">
            <TagList tags={post.tags} />
            <h1 className="font-display font-bold text-3xl sm:text-5xl tracking-tight text-fg text-balance leading-[1.08]">
              {post.title}
            </h1>
            <p className="text-lg text-muted text-pretty">{post.description}</p>
            <PostMeta post={post} full />
          </header>

          {post.cover && (
            <img
              src={post.cover.src}
              alt={post.cover.alt}
              width={post.cover.width}
              height={post.cover.height}
              fetchPriority="high"
              className="mt-8 w-full h-auto border border-dashed border-border"
            />
          )}

          <PostToc headings={post.headings} />

          <div className="mt-8">
            <MDXProvider components={mdxComponents}>
              <Suspense fallback={<div className="flex justify-center py-20"><LoadingSpinner /></div>}>
                <Body />
              </Suspense>
            </MDXProvider>
          </div>

          <footer className="no-print mt-12 pt-8 border-t border-dashed border-border flex flex-wrap items-center justify-between gap-4">
            <Link
              to={BLOG_PATH}
              className="inline-flex items-center gap-2 font-mono text-xs tracking-wider uppercase text-muted hover:text-fg transition-colors"
            >
              <ArrowLeft size={12} aria-hidden="true" /> All articles
            </Link>
            <a
              href={shareOnX(post)}
              target="_blank"
              rel="noopener noreferrer"
              className="font-mono text-xs tracking-wider uppercase text-muted hover:text-fg transition-colors"
            >
              Share on X
            </a>
          </footer>

          <PostNeighbours post={post} />
        </article>
      </div>

      {related.length > 0 && (
        <Section padding="narrow" className="no-print pb-24">
          <h2 className="font-display font-bold text-2xl text-fg tracking-tight mb-6">Related articles</h2>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {related.map((p) => (
              <PostCard key={p.slug} post={p} headingLevel="h3" />
            ))}
          </div>
        </Section>
      )}
    </Page>
  );
}
