# Writing the Technical Reference

This is the contract every chapter follows. `make whitepaper-check` enforces
the mechanical parts. The rest is what makes the book worth reading.

## Who reads this

Engineers who know distributed systems, networking and cryptography, and who
want to understand how Orama actually works: the owner re-orienting daily, a
new core developer, an auditor. Do not explain what Raft, WireGuard, a JWT or
a Merkle tree is. Do explain exactly how Orama uses each, with which
parameters, and why.

The test of a paragraph: could an expert reconstruct the mechanism, predict
its failure behaviour, and find it in the code, from what you wrote?

## Ground rules

1. **The code is the only source.** Read the code, not just the old docs. The
   old docs under `docs/` are leads to check, and many are stale. Where they
   disagree with the code, the code wins and the book says what the code does.
2. **Present tense, what exists today.** No roadmap, no "will", no "planned".
   Something designed but not built goes in Known gaps, stated as a gap.
3. **Precise numbers with their source.** "Probes every 10 s; suspect after 3
   misses, dead after 12 (`core/pkg/peerhealth/monitor.go:Config`)." Never
   "periodically" or "a few".
4. **State limits as plainly as capabilities.** An expert trusts a document
   that tells them where the system is weak.
5. **No marketing, no hedging, no filler.** No "robust", "seamless",
   "powerful", "simply". No "it should be noted that".

## Chapter skeleton

A chapter is one Markdown file. It starts with a level-1 heading (the chapter
title exactly as in `book.yaml`), followed immediately by the At-a-glance
block, then the sections below as level-2 headings, in this order and with
these exact names. Use level-3 and level-4 headings freely inside them.

```markdown
# Cluster state

> **At a glance.**
>
> - **What:** two or three sentences on what this subsystem is.
> - **Key numbers:** the ports, timeouts, limits and sizes that matter.
> - **Code:** the main packages.
> - **Depends on:** chapters this one builds on.

![The one diagram to remember](../diagrams/ch07-overview.svg)

## Why it exists
## The model
## How it works
## State it owns
## Lifecycle
## Failure modes
## Trust and security
## Limits and scale
## Design decisions
## Known gaps
## Verify it yourself
```

What goes in each section:

- **Why it exists.** The problem and the constraints that shaped the
  solution. Half a page.
- **The model.** The vocabulary and the concepts, defined once, and the
  component diagram. After this section the reader knows every noun the rest
  of the chapter uses.
- **How it works.** The mechanisms, one level-3 heading each. This is the
  bulk of the chapter. Use sequence and state diagrams for flows and state
  machines. Give the algorithm, not a summary of it.
- **State it owns.** A table of every table, file, port, key and in-memory
  structure the subsystem owns: what it holds, who writes it, who reads it,
  where it lives.
- **Lifecycle.** Boot, normal operation, rolling upgrade (mixed versions),
  restart, node loss.
- **Failure modes.** A table: *Trigger / What the system does / What you
  observe*. Cover partition, crash, disk full, clock skew, slow peer, bad
  input, and anything specific to the subsystem.
- **Trust and security.** The trust boundaries, who is authenticated how,
  what an attacker in each position can and cannot do.
- **Limits and scale.** The hard numbers, and what happens at 10x the current
  load or fleet size. Name the first bottleneck.
- **Design decisions.** Each as a level-3 heading with *Chosen*, *Rejected*,
  *Why*. Only real decisions you can see in the code or its comments.
- **Known gaps.** A bullet list. Each bullet names the gap, its consequence,
  and the code location. These are collected into Appendix H.
- **Verify it yourself.** The tests and fleet e2e features that exercise the
  subsystem, and the read-only CLI commands or queries that show it live.

`narrative` chapters (1 and 3) keep the At-a-glance block and use whatever
level-2 headings tell the story best.

Length: 4,000 to 8,000 words for most chapters; the large subsystems
(gateway, namespaces, identity, chain architecture) may run to 12,000.

## Code anchors

Refer to code with a backticked repository path, optionally followed by a
colon and a Go/TS/Zig identifier:

- `core/pkg/rqlite/eviction.go`
- `core/pkg/rqlite/eviction.go:SafeToRemoveVoter`
- `core/pkg/namespace/` (a directory)

The gate checks every such anchor: the path must exist, and the identifier
must appear in the file. **Never use line numbers**: they rot on every edit.
Paths start at the repository root (`core/`, `chain/`, `vault/`, `sdk/`,
`sdk-vault/`, `caddy/`, `e2e/`, `contracts/`).

## Diagrams

Diagrams are [D2](https://d2lang.com) source files in `diagrams/`, named
`chNN-<name>.d2` (`ch07-raft-boot.d2`). The build renders each to an SVG next
to it; a chapter embeds the SVG as an image whose alt text is the caption:

```markdown
![Raft boot: bootstrap versus rejoin](../diagrams/ch07-raft-boot.svg)
```

Every subsystem chapter has at least an overview (component) diagram and a
diagram for each non-trivial flow or state machine. Conventions:

- Start the file with `direction: right` or `direction: down`.
- Use plain shapes; `shape: cylinder` for stores, `shape: queue` for
  queues, `shape: person` for actors, `shape: sequence_diagram` for
  sequences. No icons, no colours beyond the defaults, no `style` blocks,
  so diagrams print well in black and white.
- Label edges with what flows and how (`HTTP /v1/internal/... (MAC v2)`).
- Validate with `d2 --layout elk <file>.d2 /tmp/x.svg` before finishing.
- Never hand-edit a rendered SVG. `make whitepaper-diagrams` renders them
  and stamps the source hash the gate checks.

## Cross-references

Link to another chapter by its file name, with an optional heading anchor:
`[the boot graph](04-the-node-as-a-supervisor.md#the-boot-graph)`. Inside a
volume use the bare file name; across volumes use `../vol2/39-...md`.
Anchors are GitHub-style slugs of the heading text (lower case, spaces to
hyphens, punctuation dropped). Link to appendices the same way
(`../appendices/a-port-map.md`).

## Markdown restrictions

The website renders chapters through MDX, so the source must stay plain
CommonMark with GFM tables:

- no raw HTML or JSX, no HTML comments;
- no curly braces outside code spans and code blocks;
- no bare `<` outside code; write "less than" or use a code span;
- no footnotes, no definition lists, no heading attributes (`{#id}`).

Use fenced code blocks with a language tag. Use tables for anything with
three or more parallel attributes.

## Voice

Plain declarative sentences. Active voice. One idea per sentence where
possible. Explain the reason for every non-obvious choice the code makes. Use
"we" sparingly and never for the reader. Prefer "the leader writes the row"
to "the row is written".
