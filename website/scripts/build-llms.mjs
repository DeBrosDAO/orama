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
const CHAIN_DOCS = resolve(HERE, "../src/docs/blockchain");
const SITE_DOCS = resolve(HERE, "../src/docs");
const DIST = resolve(HERE, "../dist");
const BASE = "https://orama.network";

const PROJECT = "Orama Network";
const SUMMARY =
  "Orama Network is a decentralized platform for deploying web apps, " +
  "SQLite databases and serverless WASM functions across a peer-to-peer node " +
  "network, reached through a single API gateway per namespace. A separate " +
  "Cosmos SDK ledger, oramad, mints the ORAMA token. Application requests do " +
  "not pass through that ledger. Custom domains can be verified, but " +
  "certificates are issued only on the network's own domain.";

// section -> [ [sourceDocPath, slug, title, description] ]
const MANIFEST = {
  Deploying: [
    ["DEPLOYMENT_GUIDE.md", "deploying-apps", "Deploying Apps", "Deploy static, Next.js, Go, and Node.js apps; manage SQLite databases and custom domains via the orama CLI."],
    ["SERVERLESS.md", "functions", "Serverless Functions", "Write, deploy, and invoke WASM functions; host-function API, secrets, pubsub triggers, lifecycle."],
    ["DEV_DEPLOY.md", "release-and-rollout", "Release & Rollout", "Build binaries, deploy to VPS nodes, enroll OramaOS (in development), and run rolling cluster upgrades."],
  ],
  Reference: [
    ["CLIENT_SURFACE.md", "client-surface", "Client Surface", "Humans use the orama CLI; programs use the SDK and gateway HTTP. No dashboard, no Orama MCP."],
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

  const chainPages = [
    ["what-it-is.mdx", "blockchain-what-it-is", "The Orama chain", "What oramad is, which modules are wired, and what is deliberately absent."],
    ["modules.mdx", "blockchain-modules", "Chain modules", "Every module in chain/x, what it does and whether it is live."],
    ["supply.mdx", "blockchain-supply", "ORAMA supply", "norama, the epoch schedule, the split, and which shares are actually minted."],
    ["fees.mdx", "blockchain-fees", "Chain fees", "Base fee burn, tips, state deposits, and earnings accounts."],
    ["validators.mdx", "blockchain-validators", "Validators and staking", "x/power, the bootstrap committee and hand-over, rewards, slashing, validator key moves."],
    ["governance.mdx", "blockchain-governance", "Governance", "The two houses, proposal types, tiers, timelocks and what a proposal can change."],
    ["nodes.mdx", "blockchain-nodes", "Nodes, roles and bonds", "The x/nodes registry: operators, nodes, roles, bonds and network identity."],
    ["storage-deals.mdx", "blockchain-storage-deals", "Storage deals", "Escrow, challenges, proofs, settlement and sealing."],
    ["archive.mdx", "blockchain-archive", "History archive", "Archived block ranges and pruning."],
    ["relay-rewards.mdx", "blockchain-relay-rewards", "Relay rewards", "The Tor relay registry and how rewards would be paid."],
    ["token-factory.mdx", "blockchain-token-factory", "Token factory", "Creating tokens with factory denoms."],
    ["nfts-and-market.mdx", "blockchain-nfts-and-market", "NFTs and market", "Compressed NFTs, listings, bids and royalties."],
    ["shielded.mdx", "blockchain-shielded", "Shielded pool", "Private ORAMA: messages, turnstile, cap, verifiers."],
    ["contracts.mdx", "blockchain-contracts", "Contracts", "CosmWasm, the five standard contracts and the Orama bindings."],
    ["reading-the-chain.mdx", "blockchain-reading-the-chain", "Reading the chain", "orama chain, the gateway proxy, the SDK and the explorer."],
    ["running.mdx", "blockchain-running", "Running a chain node", "Ports, build targets, localnet and the stagenet script. orama node install does not start the chain."],
  ];
  lines.push("## Blockchain", "");
  for (const [srcName, slug, title, desc] of chainPages) {
    const src = join(CHAIN_DOCS, srcName);
    if (!existsSync(src)) {
      throw new Error(`build-llms: source doc missing: ${src} (referenced by "${title}")`);
    }
    copyFileSync(src, join(llmsDir, `${slug}.md`));
    lines.push(`- [${title}](${BASE}/llms/${slug}.md): ${desc}`);
  }
  lines.push("");

  // The website's own pages that an agent installing or operating a cluster, or
  // using RootWallet or the privacy network, needs. Path is relative to src/docs.
  const sitePages = {
    "Running a cluster": [
      ["operator/getting-started.mdx", "op-what-you-need", "What you need to run a cluster", "Machines, ports, domain, your computer and the build toolchain."],
      ["operator/install-from-scratch.mdx", "op-install-from-scratch", "Install a cluster from scratch", "Every step from empty servers to a deployed site, with checks and recovery."],
      ["operator/build-and-sign.mdx", "op-build-and-sign", "Build and sign", "orama build, the signed archive and the archive trust anchor."],
      ["operator/nameserver.mdx", "op-dns", "DNS and nameservers", "Base domain, NS records and glue, delegation."],
      ["operator/tls-certificates.mdx", "op-tls", "TLS and certificates", "The shared certificate store and choosing the ACME CA."],
      ["operator/joining-nodes.mdx", "op-joining-nodes", "Joining nodes", "Invites, roles and removing a node."],
      ["operator/upgrades.mdx", "op-upgrades", "Upgrades", "Rolling upgrade protocol."],
      ["operator/monitoring.mdx", "op-monitoring", "Monitoring", "orama monitor, telemetry, alerts, the public status page."],
      ["operator/troubleshooting.mdx", "op-troubleshooting", "Troubleshooting", "Named failures and fixes."],
      ["operator/global-nodes.mdx", "op-global-nodes", "Global nodes", "Run the chain and its services."],
    ],
    "Security and privacy": [
      ["architecture/security-model.mdx", "arch-security-model", "Security model", "What is protected, how, and where it stops."],
      ["architecture/secrets-and-keys.mdx", "arch-secrets-and-keys", "Secrets and keys", "Every secret and key and where it lives."],
      ["privacy/overview.mdx", "privacy-overview", "Privacy network overview", "What is hidden from whom, and what is not built."],
      ["rootwallet/overview.mdx", "rootwallet-overview", "RootWallet", "The wallet and agent that sign you in."],
      ["rootwallet/agent.mdx", "rootwallet-agent", "The RootWallet agent", "How other programs use it."],
    ],
  };
  let siteCount = 0;
  for (const [section, entries] of Object.entries(sitePages)) {
    lines.push(`## ${section}`, "");
    for (const [rel, slug, title, desc] of entries) {
      const src = join(SITE_DOCS, rel);
      if (!existsSync(src)) {
        throw new Error(`build-llms: source doc missing: ${src} (referenced by "${title}")`);
      }
      copyFileSync(src, join(llmsDir, `${slug}.md`));
      lines.push(`- [${title}](${BASE}/llms/${slug}.md): ${desc}`);
      siteCount++;
    }
    lines.push("");
  }

  writeFileSync(join(DIST, "llms.txt"), lines.join("\n"));
  const count = Object.values(MANIFEST).reduce((n, e) => n + e.length, 0) + chainPages.length + siteCount;
  console.log(`build-llms: wrote llms.txt + ${count} docs to ${llmsDir}`);
}

build();
