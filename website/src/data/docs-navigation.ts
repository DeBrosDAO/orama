import { PERSONA_ORDER, type Persona } from "../types/persona";
import type { LucideIcon } from "lucide-react";
import {
  Activity,
  ArrowUpCircle,
  Binary,
  BookOpen,
  Boxes,
  Braces,
  Bell,
  Box,
  Brush,
  Cable,
  CheckCircle2,
  Coins,
  Compass,
  Cpu,
  Database,
  DatabaseBackup,
  Eraser,
  EyeOff,
  FileCode,
  FileText,
  FlaskConical,
  Gavel,
  Globe,
  Globe2,
  HardDrive,
  Hash,
  Hammer,
  KeyRound,
  Laptop,
  Layers,
  LayoutDashboard,
  Lock,
  MessageSquare,
  MonitorPlay,
  Network,
  Package,
  PlugZap,
  Play,
  Radio,
  Rocket,
  Route,
  ScrollText,
  Search,
  Server,
  ServerCog,
  Shield,
  ShieldCheck,
  Smartphone,
  Terminal,
  TestTube2,
  Upload,
  UserCheck,
  Users,
  Video,
  Wallet,
  Wrench,
  Zap,
} from "lucide-react";

export interface DocLink {
  title: string;
  slug: string;
  icon: LucideIcon;
  description: string;
  /** Small heading shown above the first link of a group in the sidebar. */
  group?: string;
}

/* One flat, ordered list per section. `group` only labels runs of links. */

export const START_DOCS: DocLink[] = [
  { title: "What is Orama", slug: "start/what-is-orama", icon: BookOpen, description: "What it is, how it is built, what it is not", group: "Start here" },
  { title: "Choose your path", slug: "start/journeys", icon: Compass, description: "Reading orders for developers, operators and more", group: "Start here" },
  { title: "What works today", slug: "start/status", icon: CheckCircle2, description: "Live, partial and not built", group: "Start here" },
  { title: "Glossary", slug: "start/glossary", icon: Hash, description: "The words these docs use", group: "Start here" },
];

export const DEVELOPER_DOCS: DocLink[] = [
  { title: "Quickstart", slug: "developer/getting-started", icon: Rocket, description: "Install, sign in, deploy", group: "Get started" },
  { title: "Namespaces", slug: "developer/namespaces", icon: Boxes, description: "Your own cluster and its lifecycle", group: "Get started" },
  { title: "Sign-in and sessions", slug: "developer/sign-in", icon: KeyRound, description: "Wallets, the lobby, sign-in policy, open sign-in", group: "Get started" },
  { title: "Devices", slug: "developer/devices", icon: Smartphone, description: "Device-bound sessions and approval", group: "Get started" },
  { title: "Access control and keys", slug: "developer/access-control", icon: Users, description: "Roles, members, scoped API keys, audit", group: "Get started" },
  { title: "How you talk to the network", slug: "developer/client-surface", icon: Cable, description: "CLI, SDK and HTTP. No dashboard", group: "Get started" },

  { title: "Deployments", slug: "developer/deployments", icon: Package, description: "Static, Next.js, Go and Node.js apps", group: "Build" },
  { title: "Domains", slug: "developer/domains", icon: Globe, description: "Automatic names and custom domains", group: "Build" },
  { title: "Databases", slug: "developer/databases", icon: Database, description: "RQLite and per-app SQLite", group: "Build" },
  { title: "Cache", slug: "developer/cache", icon: Zap, description: "Distributed key-value with Olric", group: "Build" },
  { title: "Storage", slug: "developer/storage", icon: HardDrive, description: "Files on IPFS, pinning, fetch capabilities", group: "Build" },
  { title: "Pub/sub", slug: "developer/pubsub", icon: MessageSquare, description: "Real-time messaging and presence", group: "Build" },
  { title: "Functions", slug: "developer/functions", icon: Braces, description: "Serverless WebAssembly", group: "Build" },
  { title: "Push notifications", slug: "developer/push", icon: Bell, description: "APNs, ntfy and Expo", group: "Build" },
  { title: "WebRTC", slug: "developer/webrtc", icon: Video, description: "Voice, video and data channels", group: "Build" },
  { title: "Vault", slug: "developer/vault", icon: Lock, description: "Secrets split across guardians", group: "Build" },

  { title: "Backups and restore", slug: "developer/backups", icon: DatabaseBackup, description: "Sealed namespace backups", group: "Run your app" },

  { title: "TypeScript SDK", slug: "developer/sdk-reference", icon: FileCode, description: "The JavaScript and TypeScript client", group: "Reference" },
  { title: "Go client", slug: "developer/go-sdk", icon: Binary, description: "The Go client for a gateway", group: "Reference" },
  { title: "Gateway HTTP API", slug: "developer/api-surface", icon: Network, description: "Every route and who owns it", group: "Reference" },
  { title: "CLI overview", slug: "developer/cli-overview", icon: Terminal, description: "Install, global flags, exit codes", group: "Reference" },
  { title: "orama app", slug: "developer/cli/app", icon: Terminal, description: "Generated from the command tree", group: "CLI commands" },
  { title: "orama auth", slug: "developer/cli/auth", icon: Terminal, description: "Sign in and sessions", group: "CLI commands" },
  { title: "orama chain", slug: "developer/cli/chain", icon: Terminal, description: "Read the chain", group: "CLI commands" },
  { title: "orama cluster, operator", slug: "developer/cli/cluster", icon: Terminal, description: "Cluster settings and operators", group: "CLI commands" },
  { title: "orama db", slug: "developer/cli/db", icon: Terminal, description: "SQLite databases", group: "CLI commands" },
  { title: "orama deploy, domain", slug: "developer/cli/deploy", icon: Terminal, description: "Deploy and domains", group: "CLI commands" },
  { title: "orama network", slug: "developer/cli/network", icon: Terminal, description: "Networks", group: "CLI commands" },
  { title: "orama function", slug: "developer/cli/function", icon: Terminal, description: "Serverless functions", group: "CLI commands" },
  { title: "orama global", slug: "developer/cli/global", icon: Terminal, description: "Global nodes and chain messages", group: "CLI commands" },
  { title: "orama members, audit", slug: "developer/cli/members", icon: Terminal, description: "Namespace members and audit", group: "CLI commands" },
  { title: "orama status", slug: "developer/cli/status", icon: Terminal, description: "Nodes, cluster, chain and account", group: "CLI commands" },
  { title: "orama namespace", slug: "developer/cli/namespace", icon: Terminal, description: "Namespaces, keys, backups", group: "CLI commands" },
  { title: "orama node", slug: "developer/cli/node", icon: Terminal, description: "Nodes", group: "CLI commands" },
  { title: "orama maint sandbox", slug: "developer/cli/sandbox", icon: Terminal, description: "Test clusters", group: "CLI commands" },
  { title: "orama storage", slug: "developer/cli/storage", icon: Terminal, description: "Storage deals", group: "CLI commands" },
  { title: "Other commands", slug: "developer/cli/other", icon: Terminal, description: "build, push, rollout, status, ssh and more", group: "CLI commands" },
  { title: "Video tutorials", slug: "developer/video-tutorials", icon: MonitorPlay, description: "Step-by-step video guides", group: "More" },
];

export const OPERATOR_DOCS: DocLink[] = [
  { title: "What you need", slug: "operator/getting-started", icon: Play, description: "Machines, domain and your computer", group: "Plan" },
  { title: "One-VPS evaluation", slug: "operator/one-vps", icon: Server, description: "Try it on one machine, not highly available", group: "Plan" },

  { title: "Install a cluster from scratch", slug: "operator/install-from-scratch", icon: ServerCog, description: "Every step, with checks and recovery", group: "Install" },
  { title: "Build and sign", slug: "operator/build-and-sign", icon: Hammer, description: "Archives and the trust anchor", group: "Install" },
  { title: "DNS and nameservers", slug: "operator/nameserver", icon: Globe2, description: "Base domain, NS records, glue", group: "Install" },
  { title: "TLS and certificates", slug: "operator/tls-certificates", icon: ShieldCheck, description: "Shared store, choosing the ACME CA", group: "Install" },
  { title: "Joining nodes", slug: "operator/joining-nodes", icon: PlugZap, description: "Invites, roles, key-only servers", group: "Install" },
  { title: "Installing by hand", slug: "operator/node-setup", icon: Wrench, description: "orama maint node install, flag by flag", group: "Install" },
  { title: "First namespace and app", slug: "operator/first-app", icon: Rocket, description: "Prove the cluster works", group: "Install" },

  { title: "Node management", slug: "operator/node-management", icon: LayoutDashboard, description: "List, ssh, status, remove", group: "Run" },
  { title: "Cluster administration", slug: "operator/cluster-admin", icon: Users, description: "Operators, creators, gateway settings", group: "Run" },
  { title: "Monitoring", slug: "operator/monitoring", icon: Activity, description: "Telemetry, alerts, status page", group: "Run" },
  { title: "Upgrades", slug: "operator/upgrades", icon: ArrowUpCircle, description: "Rolling upgrade protocol", group: "Run" },
  { title: "WireGuard", slug: "operator/wireguard", icon: Network, description: "The mesh", group: "Run" },

  { title: "Troubleshooting", slug: "operator/troubleshooting", icon: Wrench, description: "Named failures and fixes", group: "Fix" },
  { title: "Replace a node", slug: "operator/node-replacement", icon: Route, description: "Swap a nameserver safely", group: "Fix" },
  { title: "Wipe a node", slug: "operator/clean-node", icon: Eraser, description: "Full reset", group: "Fix" },
  { title: "Inspector", slug: "operator/inspector", icon: Search, description: "Deep SSH-based checks", group: "Fix" },

  { title: "OramaOS", slug: "operator/orama-os", icon: Cpu, description: "Locked-down node OS", group: "Platforms" },
  { title: "Global nodes", slug: "operator/global-nodes", icon: Globe2, description: "Run the chain and its services", group: "Platforms" },
  { title: "Hardening checklist", slug: "operator/security", icon: Shield, description: "What to verify on every node", group: "Platforms" },
];

export const ARCHITECTURE_DOCS: DocLink[] = [
  { title: "Overview", slug: "architecture/overview", icon: LayoutDashboard, description: "Planes, binaries, ports", group: "How it works" },
  { title: "Node process model", slug: "architecture/node-process-model", icon: Layers, description: "Supervisor, components, boot", group: "How it works" },
  { title: "Request lifecycle", slug: "architecture/request-lifecycle", icon: Route, description: "From HTTPS request to answer", group: "How it works" },
  { title: "Security model", slug: "architecture/security-model", icon: Shield, description: "Isolation, trust, limits", group: "Security" },
  { title: "Secrets and keys", slug: "architecture/secrets-and-keys", icon: KeyRound, description: "What is stored where", group: "Security" },
  { title: "Vulnerability disclosure", slug: "architecture/disclosure", icon: ScrollText, description: "How to report", group: "Security" },
];

export const BLOCKCHAIN_DOCS: DocLink[] = [
  { title: "What the chain is", slug: "blockchain/what-it-is", icon: Box, description: "What is wired, what is not", group: "The chain" },
  { title: "The modules", slug: "blockchain/modules", icon: Boxes, description: "Every module and its state", group: "The chain" },
  { title: "Reading the chain", slug: "blockchain/reading-the-chain", icon: Search, description: "CLI, gateway, SDK, explorer", group: "The chain" },
  { title: "Supply and emission", slug: "blockchain/supply", icon: Coins, description: "Halving schedule and the split", group: "Economics" },
  { title: "Fees", slug: "blockchain/fees", icon: Coins, description: "Base fee, tips, earnings", group: "Economics" },
  { title: "Validators and staking", slug: "blockchain/validators", icon: ShieldCheck, description: "Power, hand-over, slashing", group: "Consensus" },
  { title: "Governance", slug: "blockchain/governance", icon: Gavel, description: "The two houses", group: "Consensus" },
  { title: "Nodes, roles and bonds", slug: "blockchain/nodes", icon: Server, description: "The node registry", group: "Services" },
  { title: "Storage deals", slug: "blockchain/storage-deals", icon: HardDrive, description: "Escrow, proofs, repair", group: "Services" },
  { title: "History archive", slug: "blockchain/archive", icon: ScrollText, description: "Pruning with proofs", group: "Services" },
  { title: "Relay rewards", slug: "blockchain/relay-rewards", icon: Radio, description: "Tor relay registry and pay", group: "Services" },
  { title: "Token factory", slug: "blockchain/token-factory", icon: Coins, description: "Create tokens", group: "Assets" },
  { title: "NFTs and market", slug: "blockchain/nfts-and-market", icon: Brush, description: "Compressed NFTs, listings, royalties", group: "Assets" },
  { title: "Shielded pool", slug: "blockchain/shielded", icon: EyeOff, description: "Private ORAMA", group: "Assets" },
  { title: "Contracts", slug: "blockchain/contracts", icon: FileCode, description: "CosmWasm and bindings", group: "Assets" },
  { title: "Running a chain node", slug: "blockchain/running", icon: Terminal, description: "Ports, build, localnet", group: "Run" },
  { title: "Run a global node", slug: "blockchain/run-a-global-node", icon: Server, description: "The L1 and its services on one machine", group: "Run" },
  { title: "Security disclosure", slug: "blockchain/security-disclosure", icon: FileCode, description: "Scope, severity and how to report", group: "Run" },
];

export const PRIVACY_DOCS: DocLink[] = [
  { title: "Overview", slug: "privacy/overview", icon: EyeOff, description: "What is hidden from whom, what is not built" },
  { title: "Tor", slug: "privacy/tor", icon: Globe2, description: "Anonymity proxy, tunnel, onion submission" },
  { title: "Relayed fetch", slug: "privacy/relayed-fetch", icon: Route, description: "Download without revealing your address" },
  { title: "Stealth TURN", slug: "privacy/stealth-turn", icon: Video, description: "Calls that look like HTTPS" },
  { title: "VPN", slug: "privacy/vpn", icon: Network, description: "Status: not built" },
];

export const ROOTWALLET_DOCS: DocLink[] = [
  { title: "Overview", slug: "rootwallet/overview", icon: Wallet, description: "What it is and its three surfaces", group: "Use it" },
  { title: "Install", slug: "rootwallet/install", icon: Upload, description: "Desktop, mobile and CLI", group: "Use it" },
  { title: "Wallets and accounts", slug: "rootwallet/wallets", icon: UserCheck, description: "Create, import, lock, chains", group: "Use it" },
  { title: "Send, swap and ORAMA", slug: "rootwallet/assets", icon: Coins, description: "Money features", group: "Use it" },
  { title: "The vault", slug: "rootwallet/vault", icon: Lock, description: "Passwords, SSH keys, TOTP", group: "Use it" },
  { title: "The agent", slug: "rootwallet/agent", icon: Cpu, description: "How other programs use it", group: "With Orama" },
  { title: "Signing in to Orama", slug: "rootwallet/orama-sign-in", icon: KeyRound, description: "How the CLI signs in", group: "With Orama" },
  { title: "CLI (rw)", slug: "rootwallet/cli", icon: Terminal, description: "Every command", group: "Reference" },
  { title: "Security", slug: "rootwallet/security", icon: Shield, description: "Model, limits, audit status", group: "Reference" },
];

export const CONTRIBUTOR_DOCS: DocLink[] = [
  { title: "Dev setup", slug: "contributor/dev-setup", icon: Laptop, description: "Local development environment" },
  { title: "Code style", slug: "contributor/code-style", icon: FileText, description: "Coding conventions" },
  { title: "Testing", slug: "contributor/testing", icon: FlaskConical, description: "Unit, fleet e2e and the coverage gate" },
  { title: "Deployment", slug: "contributor/deployment", icon: Upload, description: "Build, push, rollout" },
  { title: "Sandbox clusters", slug: "contributor/sandbox", icon: TestTube2, description: "Ephemeral Hetzner clusters" },
];

/** Lookup table: section to its ordered doc list. */
export const PERSONA_DOCS: Record<Persona, DocLink[]> = {
  start: START_DOCS,
  developer: DEVELOPER_DOCS,
  operator: OPERATOR_DOCS,
  architecture: ARCHITECTURE_DOCS,
  blockchain: BLOCKCHAIN_DOCS,
  privacy: PRIVACY_DOCS,
  rootwallet: ROOTWALLET_DOCS,
  contributor: CONTRIBUTOR_DOCS,
};

/** All docs across all sections (for search). */
export const ALL_DOCS: { link: DocLink; persona: Persona }[] = PERSONA_ORDER.flatMap(
  (persona) => PERSONA_DOCS[persona].map((link) => ({ link, persona })),
);

/** First slug of each section, used when switching sections. */
export const PERSONA_FIRST_SLUG: Record<Persona, string> = Object.fromEntries(
  PERSONA_ORDER.map((persona) => [persona, PERSONA_DOCS[persona][0].slug]),
) as Record<Persona, string>;

/* DOCS_SECTIONS is used by docs.tsx to map a slug to its title. */
export interface DocSection {
  title: string;
  persona: Persona;
  links: DocLink[];
}

const SECTION_TITLES: Record<Persona, string> = {
  start: "Start here",
  developer: "Developer",
  operator: "Operator",
  architecture: "Architecture and security",
  blockchain: "Blockchain",
  privacy: "Privacy network",
  rootwallet: "RootWallet",
  contributor: "Contributor",
};

export const DOCS_SECTIONS: DocSection[] = PERSONA_ORDER.map((persona) => ({
  title: SECTION_TITLES[persona],
  persona,
  links: PERSONA_DOCS[persona],
}));
