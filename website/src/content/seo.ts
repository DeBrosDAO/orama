import { BLOG_FEED_PATH, BLOG_PATH } from "../blog/posts";
import { OG_SIZE } from "./pages";
import type { PageMeta } from "./pages";
import { ANCHAT_GROUP_URL, CONTACT_EMAIL, GITHUB_URL, SITE_NAME, SITE_URL, X_URL } from "./site";

/**
 * What each page tells search engines and link previews beyond its title and
 * description: schema.org structured data (JSON-LD) and the extra <head>
 * tags. Only facts that are true today go here.
 */

const ORG_ID = `${SITE_URL}/#organization`;
const WEBSITE_ID = `${SITE_URL}/#website`;
const BLOG_ID = `${SITE_URL}${BLOG_PATH}#blog`;

/** Robots directives for indexable pages: allow large image previews and full snippets. */
export const ROBOTS_INDEX = "index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1";
export const ROBOTS_NOINDEX = "noindex, follow";

const ORGANIZATION = {
  "@type": "Organization",
  "@id": ORG_ID,
  name: SITE_NAME,
  url: `${SITE_URL}/`,
  logo: { "@type": "ImageObject", url: `${SITE_URL}/logo.png` },
  email: CONTACT_EMAIL,
  sameAs: [GITHUB_URL, X_URL, ANCHAT_GROUP_URL],
};

const WEBSITE = {
  "@type": "WebSite",
  "@id": WEBSITE_ID,
  name: SITE_NAME,
  url: `${SITE_URL}/`,
  publisher: { "@id": ORG_ID },
  inLanguage: "en",
};

const SOURCE_CODE = {
  "@type": "SoftwareSourceCode",
  name: SITE_NAME,
  codeRepository: GITHUB_URL,
  programmingLanguage: "Go",
  license: "https://www.gnu.org/licenses/agpl-3.0.html",
  url: `${SITE_URL}/`,
  publisher: { "@id": ORG_ID },
};

const BLOG = {
  "@type": "Blog",
  "@id": BLOG_ID,
  name: `${SITE_NAME} blog`,
  url: `${SITE_URL}${BLOG_PATH}`,
  publisher: { "@id": ORG_ID },
  inLanguage: "en",
};

export const absoluteUrl = (path: string) => (path === "/" ? `${SITE_URL}/` : `${SITE_URL}${path}`);

function breadcrumbList(page: PageMeta) {
  return {
    "@type": "BreadcrumbList",
    "@id": `${absoluteUrl(page.path)}#breadcrumb`,
    itemListElement: page.crumbs.map((c, i) => ({
      "@type": "ListItem",
      position: i + 1,
      name: c.name,
      item: absoluteUrl(c.path),
    })),
  };
}

function image(page: PageMeta) {
  return { "@type": "ImageObject", url: absoluteUrl(page.image.path), ...OG_SIZE };
}

function article(page: PageMeta, lastmod: string | undefined) {
  const url = absoluteUrl(page.path);
  const post = page.post;
  const base = {
    "@id": `${url}#article`,
    headline: post?.title ?? page.crumbs.at(-1)?.name,
    description: page.description,
    url,
    mainEntityOfPage: { "@id": `${url}#webpage` },
    author: { "@id": ORG_ID },
    publisher: { "@id": ORG_ID },
    image: post?.cover ? [image(page), absoluteUrl(post.cover.src)] : image(page),
    inLanguage: "en",
  };
  if (!post) return { "@type": "TechArticle", ...base, ...(lastmod && { dateModified: lastmod }) };
  return {
    "@type": "BlogPosting",
    ...base,
    datePublished: post.date,
    dateModified: post.updated ?? post.date,
    keywords: post.tags,
    wordCount: post.wordCount,
    timeRequired: `PT${post.readingMinutes}M`,
    isPartOf: { "@id": BLOG_ID },
  };
}

/**
 * The page's JSON-LD graph. lastmod is the date the page's sources last
 * changed (from the sitemap), used where schema.org has a field for it.
 */
export function structuredData(page: PageMeta, lastmod?: string): string {
  const url = absoluteUrl(page.path);
  const isHome = page.path === "/";
  const isArticle = page.schema === "BlogPosting" || page.schema === "TechArticle";
  const webPage = {
    "@type": page.schema === "CollectionPage" ? "CollectionPage" : "WebPage",
    "@id": `${url}#webpage`,
    url,
    name: page.title,
    description: page.description,
    isPartOf: { "@id": WEBSITE_ID },
    primaryImageOfPage: image(page),
    inLanguage: "en",
    ...(!isHome && { breadcrumb: { "@id": `${url}#breadcrumb` } }),
    ...(isArticle && { mainEntity: { "@id": `${url}#article` } }),
    ...(page.path === BLOG_PATH && { mainEntity: { "@id": BLOG_ID } }),
  };
  const graph: object[] = [ORGANIZATION, WEBSITE, webPage];
  if (isHome) graph.push(SOURCE_CODE);
  else graph.push(breadcrumbList(page));
  if (isArticle) graph.push(article(page, lastmod));
  if (page.path.startsWith(BLOG_PATH)) graph.push(BLOG);
  // "<" is escaped so no string in the data can close the <script> element.
  return JSON.stringify({ "@context": "https://schema.org", "@graph": graph }).replace(/</g, "\\u003c");
}

const escapeAttr = (s: string) =>
  s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

/**
 * <head> tags that vary by page kind and are not in index.html: article
 * dates and tags, and the blog's feed. The prerenderer inserts them; the
 * fixed tags (title, description, canonical, og:*) it replaces in place.
 */
export function extraHeadTags(page: PageMeta): string {
  const tags: string[] = [];
  const post = page.post;
  if (post) {
    tags.push(`<meta property="article:published_time" content="${post.date}" />`);
    tags.push(`<meta property="article:modified_time" content="${post.updated ?? post.date}" />`);
    tags.push(`<meta property="article:author" content="${escapeAttr(SITE_NAME)}" />`);
    tags.push(`<meta property="article:section" content="Blog" />`);
    for (const t of post.tags) tags.push(`<meta property="article:tag" content="${escapeAttr(t)}" />`);
  }
  if (page.path.startsWith(BLOG_PATH)) {
    tags.push(
      `<link rel="alternate" type="application/rss+xml" title="${escapeAttr(SITE_NAME)} blog" href="${absoluteUrl(BLOG_FEED_PATH)}" />`,
    );
  }
  return tags.join("\n    ");
}
