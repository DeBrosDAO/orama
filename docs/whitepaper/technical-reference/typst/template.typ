// Book template for the Orama Whitepaper — Technical Reference.
//
// The build (core/tools/whitepaper) converts each Markdown file to Typst with
// pandoc and writes one main file per volume that applies `book` and lists
// `#part` dividers and chapter includes. Edit the look here; never edit the
// generated files under .build/.

#let ink = rgb("#1b2a3a")
#let accent = rgb("#2f5d8a")
#let rule-color = luma(200)
#let code-fill = luma(246)

#let body-font = "Libertinus Serif"
#let mono-font = "DejaVu Sans Mono"

// The At-a-glance box that opens every chapter.
#let glance(body) = block(
  width: 100%,
  fill: luma(243),
  stroke: (left: 2.5pt + accent),
  inset: (x: 12pt, y: 10pt),
  radius: 1pt,
  breakable: false,
  {
    text(weight: "bold", fill: accent, size: 9pt, tracking: 0.08em)[AT A GLANCE]
    v(4pt)
    set text(size: 9.5pt)
    body
  },
)

// A part divider: a page of its own on the right.
#let part(title) = {
  pagebreak(to: "odd", weak: true)
  set page(header: none, footer: none)
  v(1fr)
  align(center, {
    text(size: 11pt, tracking: 0.2em, fill: accent)[PART]
    v(6pt)
    line(length: 30%, stroke: 0.6pt + rule-color)
    v(6pt)
    text(size: 26pt, weight: "bold", fill: ink, title)
  })
  v(2fr)
  pagebreak(to: "odd", weak: true)
}

#let title-page(title, subtitle, volume, version, commit) = {
  set page(header: none, footer: none, numbering: none)
  v(30%)
  text(size: 30pt, weight: "bold", fill: ink, title)
  v(2pt)
  text(size: 18pt, fill: accent, subtitle)
  v(14pt)
  line(length: 100%, stroke: 0.8pt + accent)
  v(10pt)
  text(size: 15pt, volume)
  v(1fr)
  grid(
    columns: (auto, 1fr),
    column-gutter: 12pt,
    row-gutter: 6pt,
    text(fill: luma(90))[Version], [#version],
    text(fill: luma(90))[Built from], raw(commit),
    text(fill: luma(90))[Source], [`docs/whitepaper/technical-reference/` in the Orama repository],
  )
  v(8pt)
  text(size: 9pt, fill: luma(90))[
    This book describes the code at the version and commit above. Every
    chapter is re-verified against the code before each release; the code is
    the source of truth.
  ]
  pagebreak()
}

// The running header shows the current chapter on the right-hand page and
// the book title on the left.
#let running-header(title) = context {
  let here-page = here().page()
  let chapters = query(heading.where(level: 1))
  let starts-here = chapters.any(h => h.location().page() == here-page)
  if starts-here { return }
  let before = chapters.filter(h => h.location().page() <= here-page)
  set text(size: 8.5pt, fill: luma(100))
  if calc.odd(here-page) and before.len() > 0 {
    let h = before.last()
    align(right, [#counter(heading).at(h.location()).first(). #h.body])
  } else {
    align(left, title)
  }
  v(-4pt)
  line(length: 100%, stroke: 0.4pt + rule-color)
}

#let book(
  title: "",
  subtitle: "",
  volume: "",
  version: "",
  commit: "",
  first-chapter: 1,
  mode: "chapters",
  body,
) = {
  set document(title: title + " — " + subtitle + " — " + volume, author: "Orama Network")
  set page(
    paper: "a4",
    margin: (inside: 28mm, outside: 22mm, top: 26mm, bottom: 26mm),
    binding: left,
    header: running-header(title + " — " + subtitle),
    footer: context align(
      if calc.odd(here().page()) { right } else { left },
      text(size: 9pt, counter(page).display()),
    ),
  )
  set text(font: body-font, size: 10.5pt, fill: ink, lang: "en", hyphenate: true)
  set par(justify: false, leading: 0.62em, spacing: 0.95em)

  show raw: set text(font: mono-font, size: 0.82em)
  show raw.where(block: true): it => block(
    width: 100%, fill: code-fill, inset: 8pt, radius: 2pt, breakable: true,
    text(size: 8pt, it),
  )
  show link: it => {
    if type(it.dest) == label { text(fill: accent, it) } else { it }
  }

  set table(stroke: (x, y) => (top: if y <= 1 { 0.6pt + rule-color } else { 0.3pt + rule-color }))
  show table: set text(size: 8.5pt)
  show table: set par(justify: false)
  show figure.where(kind: table): set block(breakable: true)
  show figure.where(kind: table): set figure(supplement: none)
  show figure: set block(above: 1.2em, below: 1.2em)
  show figure.caption: set text(size: 8.5pt, fill: luma(80))
  set list(indent: 4pt)
  set enum(indent: 4pt)

  let chapter-label = if mode == "appendices" { "Appendix" } else { "Chapter" }
  let chapter-numbering = if mode == "appendices" { "A.1" } else { "1.1" }
  set heading(numbering: chapter-numbering)
  show heading.where(level: 1): it => {
    pagebreak(to: "odd", weak: true)
    v(18mm)
    text(size: 11pt, tracking: 0.15em, fill: accent)[#upper(chapter-label) #counter(heading).display(chapter-numbering.first())]
    v(4pt)
    text(size: 24pt, weight: "bold", it.body)
    v(10mm)
  }
  show heading.where(level: 2): it => block(above: 1.6em, below: 0.8em, text(size: 14pt, weight: "bold", it))
  show heading.where(level: 3): it => block(above: 1.3em, below: 0.6em, text(size: 11.5pt, weight: "bold", it))
  show heading.where(level: 4): it => block(above: 1.1em, below: 0.5em, text(size: 10.5pt, weight: "bold", style: "italic", it.body))

  title-page(title, subtitle, volume, version, commit)

  set page(numbering: "i")
  counter(page).update(1)
  {
    show heading: none
    heading(numbering: none, outlined: false)[Contents]
  }
  text(size: 20pt, weight: "bold")[Contents]
  v(8mm)
  outline(title: none, depth: 2, indent: auto)

  set page(numbering: "1")
  counter(page).update(1)
  counter(heading).update(first-chapter - 1)
  body
}
