import { Link, useParams } from "react-router";
import { ArrowLeft } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { PostCard } from "../components/blog/post-card";
import { Pagination } from "../components/blog/pagination";
import { BLOG_PATH, POSTS, pageCount, pageOf, parsePageParam, postsWithTag, tagLabel, tagPath } from "../blog/posts";
import NotFound from "./not-found";

export default function BlogTag() {
  const params = useParams();
  const tag = params.tag ?? "";
  const page = parsePageParam(params.page);
  const tagged = postsWithTag(POSTS, tag);
  const pages = pageCount(tagged.length);
  if (tagged.length === 0 || page === null || page > pages) return <NotFound />;
  const label = tagLabel(tag);

  return (
    <Page route={{ path: tagPath(tag, page), title: label, description: "" }}>
      <PageHero
        eyebrow={page === 1 ? "Blog · Tag" : `Blog · Tag · page ${page} of ${pages}`}
        title={label}
        line={`${tagged.length} ${tagged.length === 1 ? "article" : "articles"} tagged “${label}”.`}
      >
        <Link
          to={BLOG_PATH}
          className="inline-flex items-center gap-2 font-mono text-[11px] tracking-wider uppercase text-muted hover:text-fg transition-colors"
        >
          <ArrowLeft size={12} aria-hidden="true" /> All articles
        </Link>
      </PageHero>

      <Section padding="narrow">
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {pageOf(tagged, page).map((post) => (
            <PostCard key={post.slug} post={post} />
          ))}
        </div>
        <Pagination page={page} pages={pages} pathFor={(n) => tagPath(tag, n)} />
      </Section>
    </Page>
  );
}
