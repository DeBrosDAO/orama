# Orama Whitepaper — Technical Reference

The complete, expert-level description of how the Orama Network works:
every subsystem, how it is built, how it fails and where it lives in the
code. It is written for engineers. The short whitepaper
(`docs/whitepaper/WHITEPAPER.md`) is the overview for everyone else, and the
website docs are the how-to guides.

The book describes the code at one version: the one in `/VERSION`. A release
is not done until the book has been re-verified against the code at that
version (see "Releasing" below).

## Layout

| Path | What |
|---|---|
| `book.yaml` | The manifest: volumes, parts, chapters, which code each chapter owns, and each chapter's verified-at version |
| `STYLE.md` | How a chapter is written: structure, anchors, diagrams, Markdown rules, voice |
| `vol1/`, `vol2/` | The chapters, one Markdown file each. Volume I is the platform, Volume II the global layer and the chain |
| `appendices/` | Reference appendices; most are generated from the code |
| `diagrams/` | D2 sources and their rendered, hash-stamped SVGs |
| `typst/template.typ` | The print design |

The Typst build writes to `.build/` and the PDFs to `dist/`; both are
git-ignored.

## Commands

Run from the repository root.

| Command | What it does |
|---|---|
| `make whitepaper-check` | Runs every gate. Part of `make test` |
| `make whitepaper-diagrams` | Renders changed D2 diagrams to SVG and stamps them |
| `make whitepaper-gen` | Regenerates the appendices built from code (and the CLI reference) |
| `make whitepaper` | All of the above, then typesets `dist/orama-whitepaper-technical-reference-v<version>-{vol1,vol2,appendices}.pdf` |

Tools: Go, `d2`, `pandoc`, `typst` (`brew install d2 pandoc typst`). The
gate itself needs only Go and git.

## The gates

`make whitepaper-check` (implemented in `core/tools/whitepaper`) fails when:

| Gate | Fails when |
|---|---|
| version | `book.yaml` `version`, or any chapter's `verified`, is not `/VERSION` |
| ownership | a tracked file is explained by no chapter (`owns:`) and not excluded; an `owns:` entry is too broad, duplicated, or matches nothing |
| anchors | a code anchor (`` `core/pkg/x/y.go:Name` ``) names a path that does not exist or an identifier the file does not contain |
| structure | a chapter's title, At-a-glance block or section headings depart from STYLE.md |
| markdown | a chapter uses something the website's MDX renderer cannot take |
| links | a link or image points at a missing file or heading |
| diagrams | an SVG is missing, stale against its D2 source, unused, or has no source |
| generated | a generated appendix differs from what the code produces now |
| manifest | a listed file is missing, or a book file is not listed |

## Keeping it true

The code is the source of truth. The book follows it in two ways:

1. **In the same change.** A change that alters behaviour a chapter
   describes updates that chapter. The gates catch the mechanical part: a
   renamed function breaks an anchor, a new package has no owner, a changed
   port regenerates Appendix A.
2. **At every release.** See below.

## Releasing

`make -C core bump VER=X.Y.Z` changes `/VERSION`, and from that moment the
version gate fails until the book is re-verified:

1. For each chapter, compare the chapter with the code at the new version:
   every claim, number and diagram, every Known gap. Fix what changed.
2. Set the chapter's `verified:` in `book.yaml` to the new version.
3. Set `version:` in `book.yaml` to the new version.
4. `make whitepaper` must pass and produce the three PDFs for the release.

Chapters are independent, so the re-verification parallelises well.
