// Generates the website's CLI reference pages from docs/CLI_REFERENCE.md.
//
// docs/CLI_REFERENCE.md is itself generated from the cobra command tree by
// core/cmd/orama/reference_test.go, and a test fails when it drifts from the
// code. Converting that file here keeps the website's reference from becoming a
// second copy that someone has to maintain by hand.
//
//   node scripts/build-cli-docs.mjs          write src/docs/developer/cli/*.mdx
//   node scripts/build-cli-docs.mjs --check  exit 1 if the files are out of date
//
// Run it after `make -C core docs` changes docs/CLI_REFERENCE.md, and commit the
// output. The generated pages are listed in src/data/docs-navigation.ts.

import { mkdirSync, readFileSync, writeFileSync, existsSync, readdirSync, rmSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const SOURCE = resolve(HERE, "../../docs/CLI_REFERENCE.md");
const OUT_DIR = resolve(HERE, "../src/docs/developer/cli");

/** Which page each top-level command lands on. Anything unlisted goes to "other". */
const PAGES = [
  { slug: "app", title: "orama app", blurb: "Inspect and manage deployed applications.", commands: ["app"] },
  { slug: "auth", title: "orama auth", blurb: "Sign in, approve a login from another machine, and manage sessions.", commands: ["auth"] },
  { slug: "chain", title: "orama chain", blurb: "Read the Orama chain and fund test accounts.", commands: ["chain"] },
  { slug: "cluster", title: "orama cluster and operator", blurb: "Cluster-wide settings, namespace creators, and the wallets that operate a cluster.", commands: ["cluster", "operator"] },
  { slug: "db", title: "orama db", blurb: "Create and query SQLite databases.", commands: ["db"] },
  { slug: "deploy", title: "orama deploy and domain", blurb: "Deploy apps and attach custom domains.", commands: ["deploy", "domain"] },
  { slug: "env", title: "orama env", blurb: "Choose which cluster the CLI talks to.", commands: ["env"] },
  { slug: "function", title: "orama function", blurb: "Build, deploy and run serverless functions.", commands: ["function"] },
  { slug: "global", title: "orama global", blurb: "Install and operate a global node, and build its chain messages.", commands: ["global"] },
  { slug: "members", title: "orama members and audit", blurb: "Who may work in a namespace, and the namespace's audit trail.", commands: ["members", "audit"] },
  { slug: "monitor", title: "orama monitor", blurb: "Watch cluster health from your own machine.", commands: ["monitor"] },
  { slug: "namespace", title: "orama namespace", blurb: "Create namespaces, mint API keys, back up and restore.", commands: ["namespace"] },
  { slug: "node", title: "orama node", blurb: "Install, run, upgrade and remove nodes.", commands: ["node"] },
  { slug: "sandbox", title: "orama sandbox", blurb: "Throwaway Hetzner clusters for testing.", commands: ["sandbox"] },
  { slug: "storage", title: "orama storage", blurb: "Storage deals on the Orama chain.", commands: ["storage"] },
  { slug: "other", title: "Other commands", blurb: "build, push, rollout, status, nodes, ssh, inspect, invite and version.", commands: [] },
];

/** Example addresses in command help that are real hosts become documentation-range ones. */
const EXAMPLE_ADDRESSES = new Map([
  ["57.129.166.16", "203.0.113.10"],
  ["1.2.3.4", "203.0.113.4"],
  ["5.6.7.8", "203.0.113.8"],
  ["9.10.11.12", "203.0.113.12"],
]);

function redactAddresses(text) {
  let out = text;
  for (const [from, to] of EXAMPLE_ADDRESSES) out = out.split(from).join(to);
  return out;
}

/** Escape what MDX would read as JSX or an expression, outside inline code. */
function escapeProse(line) {
  return line
    .split(/(`[^`]*`)/)
    .map((part, i) =>
      i % 2 === 1
        ? part
        : part.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/\{/g, "&#123;").replace(/\}/g, "&#125;"),
    )
    .join("");
}

/** Table cells: same escaping, and a literal pipe would end the cell. */
function escapeCell(text) {
  return escapeProse(text);
}

/** Split the generated file into [{ name, body }] by "### orama ..." headings. */
function readCommands(markdown) {
  const start = markdown.indexOf("\n---\n");
  if (start < 0) throw new Error("build-cli-docs: no '---' separator before the first command");
  const lines = markdown.slice(start + 5).split("\n");
  const commands = [];
  let current = null;
  let inFence = false;
  for (const line of lines) {
    if (line.startsWith("```")) inFence = !inFence;
    const heading = !inFence && /^### (orama.*)$/.exec(line);
    if (heading) {
      current = { name: heading[1], lines: [] };
      commands.push(current);
    } else if (current) {
      current.lines.push(line);
    }
  }
  return commands;
}

/** Render one command's body. Indented help text (examples, lists) becomes code. */
function renderBody(lines) {
  const out = [];
  let fence = false; // inside a fence that came from the source
  let block = null; // an indented run being collected
  const flush = () => {
    if (!block) return;
    while (block.length && block[block.length - 1].trim() === "") block.pop();
    const indent = Math.min(...block.filter((l) => l.trim()).map((l) => l.match(/^ */)[0].length));
    const isShell = block.some((l) => /^\s*(orama|rw|sudo|curl|#|\$)\b|^\s*#/.test(l));
    out.push(isShell ? "```bash" : "```", ...block.map((l) => l.slice(Math.min(indent, l.match(/^ */)[0].length))), "```");
    block = null;
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.startsWith("```")) {
      flush();
      fence = !fence;
      out.push(line);
      continue;
    }
    if (fence) {
      out.push(line);
      continue;
    }
    if (line.startsWith("|")) {
      flush();
      out.push(line.startsWith("|--") ? line : "| " + line.slice(1, -1).split(/(?<!\\)\|/).map((c) => escapeCell(c.trim())).join(" | ") + " |");
      continue;
    }
    const indented = /^ {2,}\S/.test(line);
    if (indented) {
      block = block ?? [];
      block.push(line);
      continue;
    }
    if (block && line.trim() === "") {
      // A blank line inside an indented run stays in it when the run continues.
      const next = lines.slice(i + 1).find((l) => l.trim() !== "");
      if (next !== undefined && /^ {2,}\S/.test(next)) {
        block.push("");
        continue;
      }
    }
    flush();
    // MDX reads a line that starts with import/export as ESM.
    out.push(escapeProse(line).replace(/^(i)(mport\b)|^(e)(xport\b)/, (m, a, b, c, d) => (a ? "&#105;" + b : "&#101;" + d)));
  }
  flush();
  return out.join("\n").replace(/\n{3,}/g, "\n\n").trim();
}

function pageFor(commandName) {
  const top = commandName.split(" ")[1];
  const page = PAGES.find((p) => p.commands.includes(top));
  return page ?? PAGES.find((p) => p.slug === "other");
}

function build() {
  if (!existsSync(SOURCE)) throw new Error(`build-cli-docs: ${SOURCE} is missing`);
  const commands = readCommands(readFileSync(SOURCE, "utf-8"));
  if (commands.length < 100) throw new Error(`build-cli-docs: only ${commands.length} commands parsed; the format changed`);

  const files = new Map();
  const byPage = new Map(PAGES.map((p) => [p.slug, []]));
  for (const cmd of commands) byPage.get(pageFor(cmd.name).slug).push(cmd);

  for (const page of PAGES) {
    const cmds = byPage.get(page.slug);
    if (cmds.length === 0) throw new Error(`build-cli-docs: page ${page.slug} has no commands`);
    const text = [
      `{/* Generated by scripts/build-cli-docs.mjs from docs/CLI_REFERENCE.md. Do not edit by hand. */}`,
      "",
      `# ${page.title}`,
      "",
      page.blurb + " Every command and flag below comes from the `orama` command tree, so it matches the binary.",
      "",
      "Global conventions are on the [CLI overview](/docs/developer/cli-overview).",
      "",
      ...cmds.flatMap((cmd) => [`## ${cmd.name}`, "", redactAddresses(renderBody(cmd.lines)), ""]),
    ].join("\n");
    files.set(`${page.slug}.mdx`, text.replace(/\n{3,}/g, "\n\n"));
  }
  return { files, count: commands.length };
}

const { files, count } = build();
if (process.argv.includes("--check")) {
  let stale = false;
  for (const [name, text] of files) {
    const path = join(OUT_DIR, name);
    if (!existsSync(path) || readFileSync(path, "utf-8") !== text) {
      console.error(`build-cli-docs: ${path} is out of date`);
      stale = true;
    }
  }
  process.exit(stale ? 1 : 0);
}
mkdirSync(OUT_DIR, { recursive: true });
for (const f of readdirSync(OUT_DIR)) if (f.endsWith(".mdx")) rmSync(join(OUT_DIR, f));
for (const [name, text] of files) writeFileSync(join(OUT_DIR, name), text);
console.log(`build-cli-docs: wrote ${files.size} pages for ${count} commands to ${OUT_DIR}`);
