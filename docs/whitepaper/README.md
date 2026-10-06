# Orama Network — Whitepaper

`WHITEPAPER.md` is the canonical source of the Orama Network whitepaper. The
website renders it at `/whitepaper`. There is no separate HTML or PDF copy to
keep in sync. Edit the Markdown and nothing else.

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
