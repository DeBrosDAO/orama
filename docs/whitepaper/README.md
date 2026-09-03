# Orama Network — Technical Whitepaper

The source of record for the Orama Network whitepaper. `whitepaper.html` is a
single self-contained file — the stylesheet is inline and the only external
dependency is Google Fonts, fetched at render time.

| File | What it is |
|------|-----------|
| `whitepaper.html` | The document. Edit this. |
| `render.mjs` | Renders the HTML to PDF via headless Chrome, with running footers and page numbers. |
| `orama-network-whitepaper.pdf` | The rendered output, committed so it can be read without a build. |

## Rebuilding the PDF

```bash
node docs/whitepaper/render.mjs docs/whitepaper/whitepaper.html docs/whitepaper/orama-network-whitepaper.pdf
```

`render.mjs` imports `puppeteer` by absolute path; adjust that import if your
install lives elsewhere. Re-render and commit the PDF whenever the HTML
changes, so the two never drift.

## What this document is, and is not

It is a technical description of a working system and an honest statement of
what it does not do. It is **not** an offering document, it does not describe a
token, and it makes no financial claim. Part V is the only forward-looking
section; every item in it is written as an open question with its blockers.

The document is deliberately monochrome and set as a two-column academic paper.
Emphasis is carried by rule weight and fill value rather than colour, so it
prints correctly in greyscale.

## Accuracy

Every factual claim was checked against the source tree rather than against
project documentation, and where the two disagreed the source won. Eighteen
claims were corrected during review after being verified against specific
files, including three that our own documentation still asserts:

- The cluster database does not enforce HTTP authentication — the config field
  that would pass credentials to it is never assigned.
- Secret custody (the vault) is not a usable service.
- The dedicated node image has never been booted.

If you change platform behaviour that this document describes, update the
document in the same change — the repository rule that documentation must not
lie applies here too.

## Companion documents

Three longer documents sit alongside this one and are referenced by it: the
*Orama Network Atlas* (architecture reference), *The Hardening Playbook*
(security series II) and *Rewrite or Replace?* (security series III). They are
not in this repository.
