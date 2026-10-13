import { useParams } from "react-router";
import { Rss } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { PostCard } from "../components/blog/post-card";
import { TagList } from "../components/blog/tag-list";
import { Pagination } from "../components/blog/pagination";
import { BLOG_FEED_PATH, POSTS, allTags, blogPagePath, pageCount, pageOf, parsePageParam } from "../blog/posts";
import NotFound from "./not-found";

const TAGS = allTags(POSTS);
const TAG_COUNTS = new Map(TAGS.map((t) => [t.tag, t.count]));

export default function Blog() {
  const page = parsePageParam(useParams().page);
  const pages = pageCount(POSTS.length);
  if (page === null || page > pages) return <NotFound />;
  const posts = pageOf(POSTS, page);

  return (
    <Page route={{ path: blogPagePath(page), title: "Blog", description: "" }}>
      <PageHero
        eyebrow={page === 1 ? "Blog" : `Blog · page ${page} of ${pages}`}
        title="Notes from the network."
        line="How a cloud with nobody in the middle is built, what runs on it, and why it matters."
      >
        <a
          href={BLOG_FEED_PATH}
          className="inline-flex items-center gap-2 font-mono text-[11px] tracking-wider uppercase text-muted hover:text-fg transition-colors"
        >
          <Rss size={12} aria-hidden="true" /> RSS feed
        </a>
      </PageHero>

      <Section padding="narrow">
        {TAGS.length > 0 && <TagList tags={TAGS.map((t) => t.tag)} counts={TAG_COUNTS} className="justify-center mb-10" />}
        {posts.length === 0 ? (
          <p className="text-center text-muted py-16">No posts yet.</p>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {posts.map((post, i) => (
              <PostCard key={post.slug} post={post} featured={page === 1 && i === 0} />
            ))}
          </div>
        )}
        <Pagination page={page} pages={pages} pathFor={blogPagePath} />
      </Section>
    </Page>
  );
}
