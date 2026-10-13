# Orama Network — Whitepaper

`WHITEPAPER.md` is the canonical source of the short overview. The website
renders it at `/whitepaper`; there is no separate HTML copy to keep in sync.
Edit the Markdown and nothing else.

The same page offers the PDF editions for download: the technical edition
(`orama-whitepaper/`) and the Technical Reference volumes
(`technical-reference/`). `website/scripts/build-whitepaper.mjs` copies them
from their gitignored `dist/` into the site at build time, and fails the build
when one is missing: run `make whitepaper whitepaper-short` from the repo root
first. The page shows each file's version (the repo's `VERSION`), size and page
count, measured from the built files.

## Format

The website renders the file through an MDX pipeline in plain `md` mode, so
the file must stay plain CommonMark with GFM tables:

- no raw HTML or JSX;
- no curly braces outside code spans or code blocks;
- no bare `<` outside code. Write "less than" or use a code span.

## What this document is

It describes the system that exists in the code today, marks what is only
partially done, and lists the roadmap separately. It is not an offering
document and makes no financial claim.

## Keeping it true

The code is the source of truth. If you change platform behaviour that the
whitepaper describes, update `WHITEPAPER.md` in the same change. A claim in
the whitepaper that the code contradicts is a defect.
