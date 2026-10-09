# Orama Whitepaper — Technical Edition

The 100-page, single-volume edition of the Orama Whitepaper. It is derived
from the [Technical Reference](../technical-reference/README.md), which
remains the deep source: that book owns the code, every subsystem in full, and
the generated appendices. This edition says the same things shorter, in 22
narrative chapters in four parts (Overview, The Platform, The Global Layer,
Reference). When the two disagree, the code decides and the Technical
Reference is corrected first.

## Layout

- `book.yaml` is the manifest: the chapter list, the per-chapter `max_words`,
  `max_words_total`, and `ownership: false` (no chapter owns code).
- `chNN-*.md` are the chapters. Each opens with its H1 and the At-a-glance
  block, then free level-2 headings. They may link to each other by bare file
  name and embed the Technical Reference's SVGs by relative path, for example
  `../technical-reference/diagrams/ch01-overview.svg`. This book has no
  diagrams of its own.
- `dist/` and `.build/` are build output and git-ignored.

## Gates

`make whitepaper-short-check` runs the same tool as the Technical Reference
with `-book docs/whitepaper/orama-whitepaper`. It checks Markdown and MDX
restrictions, chapter structure, links, version stamps (`version` and every
`verified` equal `/VERSION`), every `path:Identifier` code anchor, and the
`words` gate. It skips ownership.

The `words` gate counts prose words: lines outside fenced code blocks and
tables, with heading, quote and list markers, image syntax and link targets
removed; inline code counts. A chapter over its `max_words`, an empty chapter,
or a book over `max_words_total` fails. Fix it by cutting, not by raising the
cap.

`make whitepaper-check` (part of `make test`) runs this check and the
Technical Reference's.

## Build

```
make whitepaper-short
```

Needs pandoc and typst (and pdfinfo from poppler for the page count). It
writes `dist/orama-whitepaper-v<version>.pdf`, one volume with no appendices,
using the Technical Reference's typst template, and prints the page count. The 100-page limit includes the title page and the
table of contents; the `typeset` block in `book.yaml` (one-level contents,
tighter margins, chapters starting on the next page instead of the next right
page) keeps the book under it. Those options apply to this book only: the
Technical Reference keeps the template defaults.

## On a release

`make bump` changes `VERSION`. Re-read each chapter against the code, then set
`version` and each chapter's `verified` in `book.yaml` to the new version.
