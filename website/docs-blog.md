# Website: blog and SEO

How to write a blog post for orama.network, and what the build does for
search engines on every page. The site lives in `website/`; it is built with
`pnpm build` and published with `website/deploy.sh`.

## Writing a post

A post is one Markdown file in `website/blog/`. The file name is the address:
`website/blog/why-orama.md` is published at `https://orama.network/blog/why-orama`.
Use lowercase words joined by hyphens, and pick the words people search for;
the address never changes after publication.

```markdown
---
title: "What is Orama Network? A decentralized cloud, explained"
description: "One or two sentences, 50 to 160 characters: the snippet search results show."
date: 2026-10-09
tags: [decentralized-cloud, privacy]
---

The opening paragraph. Search engines and readers decide here.

## First section

Text, **bold**, [links](/platform), lists, tables and fenced code blocks.
```

| Key | Required | Rule |
|---|---|---|
| `title` | yes | 10–110 characters. Shown as the page's one H1. |
| `seoTitle` | when `title` is over 60 characters | 10–60 characters. Used for the browser tab and search results instead of `title`. |
| `description` | yes | 50–160 characters. Search snippet, link previews, feed. |
| `date` | yes | `YYYY-MM-DD`, the publication day. |
| `updated` | no | `YYYY-MM-DD`, on or after `date`. Set it when you change a post meaningfully; it moves the sitemap date and `dateModified`. |
| `tags` | yes | 1–6 tags, lowercase words joined by hyphens (`web3-backends`). Each tag gets a page at `/blog/tag/<tag>`, kept out of search and the sitemap until it lists 3 posts; the first tag labels the post's card. |
| `cover` | no | An image under `website/public/images/blog/` (`.png`, `.jpg`, `.webp`), e.g. `/images/blog/why-orama/cover.png`. Its size is read at build time. |
| `coverAlt` | with `cover` | A sentence describing the image. |
| `draft` | no | `true` keeps the post out of the build. |

Rules for the body:

- No `# H1`: the page shows the title as the only H1. Start sections at `##`.
- Plain Markdown with GitHub tables, not MDX: no JSX or imports.
- Link to other pages with site paths (`/platform`, `/docs/developer/functions`).
- Write only what is true of the code today, as for the rest of the site.

Unknown keys, a missing description, a description over 160 characters, a bad
date, an H1 in the body or a missing cover file fail the build with a message
naming the post and the fix.

**Publishing.** A post is published by the first build on or after its `date`
that does not have `draft: true`. A post dated in the future is a scheduled
post: it stays out until a build runs on that day. `pnpm dev` shows drafts and
scheduled posts so you can preview them.

Run `pnpm dev` in `website/` to preview, `pnpm test` to check, then deploy.

## What every page gets

The build (`website/scripts/prerender.mjs`) writes each page as real HTML
from one list, `PAGES` in `website/src/content/pages.ts`: the hand-written
pages, every doc, the explorer front page, the blog index (12 posts per page),
every tag page and every post.

- **Head.** Its own `<title>` (60 characters at most), description, canonical
  URL, `robots` (`max-image-preview:large`, full snippets), Open Graph and X
  card tags. Posts also get `og:type=article` and `article:*` dates and tags.
- **Breadcrumbs.** A visible trail (Home › Blog › Post, Home › Docs ›
  Developers › Cache) and the same trail as `BreadcrumbList` structured data.
  The home page has none; the explorer has only the structured data.
- **Structured data (JSON-LD).** `Organization` and `WebSite` everywhere;
  `BlogPosting` for posts (dates, tags, word count, reading time, image);
  `TechArticle` for docs and the whitepaper; `CollectionPage` for the blog,
  tag pages and the docs front page; `Blog` on blog pages.
- **Link-preview images.** Each post, each doc and the blog get a generated
  1200×630 card (`website/scripts/build-og.mjs`, written to `dist/og/`); other
  pages use `public/og-image.png`.
- **Sitemaps.** `/sitemap.xml` is an index of `/sitemap-pages.xml`,
  `/sitemap-docs.xml` and `/sitemap-blog.xml`. A post's `lastmod` is its
  `updated` or `date`; any other page's is the newest commit of the source
  files it is built from, leaving out the files every page shares, or the
  build day if one of them has uncommitted changes.
- **Feed.** `/blog/rss.xml`, the 50 newest posts. Submit it in Google Search
  Console as a sitemap so new posts are fetched quickly.
- **For AI agents.** `/llms.txt` lists every post, with its raw Markdown at
  `/llms/blog/<slug>.md`.
- **404.** `/404.html`, marked `noindex`, served with a 404 status for any
  address no page owns. Explorer pages under `/explorer/` other than its
  front page are sent with `X-Robots-Tag: noindex`.

The nginx site (`website/deploy/orama.network.nginx.conf`) is installed by
hand on the web host, not by `deploy.sh`; reinstall it when it changes.

## After a deploy

1. Submit `https://orama.network/sitemap.xml` and
   `https://orama.network/blog/rss.xml` in Google Search Console and Bing
   Webmaster Tools (once; they are re-read on their own).
2. Check a new post with Google's Rich Results Test and the URL Inspection
   tool.
