// Emits the agent-facing docs endpoint into the built site:
//   dist/llms.txt        — llmstxt.org index (what an LLM reads first)
//   dist/llms/<slug>.md  — raw markdown of each curated doc
//
// Source of truth is the repo-root docs/ tree (../../docs from here). We
// publish only the curated subset an agent building ON Orama needs — not the
// internal node-operations runbooks. Adding a doc to a project means adding a
// line here; a missing source file fails the build loudly (no silent skip).

import { mkdirSync, copyFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const DOCS = resolve(HERE, "../../docs");
const DIST = resolve(HERE, "../dist");
const BASE = "https://iofo4ifs.orama.network";

const PROJECT = "Orama Network";
const SUMMARY =
  "Orama Network is a decentralized platform for deploying web apps, " +
  "SQLite databases, custom domains, and serverless WASM functions across a " +
  "peer-to-peer node network, reached through a single API gateway per namespace.";

// section -> [ [sourceDocPath, slug, title, description] ]
const MANIFEST = {
  Deploying: [
    ["DEPLOYMENT_GUIDE.md", "deploying-apps", "Deploying Apps", "Deploy static, Next.js, Go, and Node.js apps; manage SQLite databases and custom domains via the orama CLI."],
    ["SERVERLESS.md", "functions", "Serverless Functions", "Write, deploy, and invoke WASM functions; host-function API, secrets, pubsub triggers, lifecycle."],
    ["DEV_DEPLOY.md", "release-and-rollout", "Release & Rollout", "Build binaries, deploy to VPS nodes, enroll OramaOS, and run rolling cluster upgrades."],
  ],
  Reference: [
    ["ARCHITECTURE.md", "architecture", "Architecture", "System architecture: gateway, namespaces, RQLite, Olric cache, IPFS storage, WASM runtime."],
    ["CLI_REFERENCE.md", "cli-reference", "CLI Reference", "Every orama command and flag, generated from the command tree."],
    ["API_SURFACE.md", "api-surface", "API Surface", "Every gateway route and which client owns it: SDK, CLI, direct, or internal."],
    ["TS_SDK.md", "typescript-sdk", "TypeScript SDK", "@debros/orama — database, pub/sub, cache, storage, functions and auth from application code."],
    ["GO_CLIENT_SDK.md", "go-client-sdk", "Go Client SDK", "Go client for talking to an Orama gateway from application code."],
    ["MONITORING.md", "monitoring", "Monitoring", "Cluster health and per-node reporting with the orama monitor / node report commands."],
    ["COMMON_PROBLEMS.md", "troubleshooting", "Troubleshooting", "Known failure modes and how to diagnose them."],
  ],
};

function build() {
  const llmsDir = join(DIST, "llms");
  mkdirSync(llmsDir, { recursive: true });

  const lines = [`# ${PROJECT}`, "", `> ${SUMMARY}`, ""];

  for (const [section, entries] of Object.entries(MANIFEST)) {
    lines.push(`## ${section}`, "");
    for (const [srcName, slug, title, desc] of entries) {
      const src = join(DOCS, srcName);
      if (!existsSync(src)) {
        throw new Error(`build-llms: source doc missing: ${src} (referenced by "${title}")`);
      }
      copyFileSync(src, join(llmsDir, `${slug}.md`));
      lines.push(`- [${title}](${BASE}/llms/${slug}.md): ${desc}`);
    }
    lines.push("");
  }

  writeFileSync(join(DIST, "llms.txt"), lines.join("\n"));
  const count = Object.values(MANIFEST).reduce((n, e) => n + e.length, 0);
  console.log(`build-llms: wrote llms.txt + ${count} docs to ${llmsDir}`);
}

build();
