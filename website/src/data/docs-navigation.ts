import type { Persona } from "../types/persona";
import type { LucideIcon } from "lucide-react";
import {
  BookOpen,
  Rocket,
  Database,
  Lock,
  MessageSquare,
  Zap,
  HardDrive,
  Code,
  Globe,
  Video,
  FileCode,
  Terminal,
  Play,
  Server,
  Activity,
  ArrowUpCircle,
  Shield,
  Wrench,
  LayoutDashboard,
  Laptop,
  FileText,
  FlaskConical,
  Upload,
  MonitorPlay,
} from "lucide-react";

export interface DocLink {
  title: string;
  slug: string;
  icon: LucideIcon;
  description: string;
}

/* Flat doc list per persona — no sections, no accordion */

export const DEVELOPER_DOCS: DocLink[] = [
  {
    title: "Introduction",
    slug: "developer/getting-started",
    icon: BookOpen,
    description: "Get started with the Orama SDK",
  },
  {
    title: "Deployments",
    slug: "developer/deployments",
    icon: Rocket,
    description: "Deploy static, Next.js, Go, Node.js apps",
  },
  {
    title: "Databases",
    slug: "developer/databases",
    icon: Database,
    description: "Distributed SQL with RQLite",
  },
  {
    title: "Vault",
    slug: "developer/vault",
    icon: Lock,
    description: "Encrypted secrets with Shamir sharing",
  },
  {
    title: "PubSub",
    slug: "developer/pubsub",
    icon: MessageSquare,
    description: "Real-time messaging and presence",
  },
  {
    title: "Cache",
    slug: "developer/cache",
    icon: Zap,
    description: "Distributed key-value with Olric",
  },
  {
    title: "Storage",
    slug: "developer/storage",
    icon: HardDrive,
    description: "Decentralized files on IPFS",
  },
  {
    title: "Functions",
    slug: "developer/functions",
    icon: Code,
    description: "Serverless WASM at the edge",
  },
  {
    title: "Domains",
    slug: "developer/domains",
    icon: Globe,
    description: "Automatic DNS and SSL",
  },
  {
    title: "WebRTC",
    slug: "developer/webrtc",
    icon: Video,
    description: "Video, voice, and data channels",
  },
  {
    title: "SDK Reference",
    slug: "developer/sdk-reference",
    icon: FileCode,
    description: "TypeScript SDK API surface",
  },
  {
    title: "CLI Reference",
    slug: "developer/cli-reference",
    icon: Terminal,
    description: "All CLI commands and flags",
  },
  {
    title: "Video Tutorials",
    slug: "developer/video-tutorials",
    icon: MonitorPlay,
    description: "Step-by-step video guides",
  },
];

export const OPERATOR_DOCS: DocLink[] = [
  {
    title: "Getting Started",
    slug: "operator/getting-started",
    icon: Play,
    description: "Operator onboarding guide",
  },
  {
    title: "Node Setup",
    slug: "operator/node-setup",
    icon: Server,
    description: "Install and configure a node",
  },
  {
    title: "Node Management",
    slug: "operator/node-management",
    icon: LayoutDashboard,
    description: "Unified commands for managing your nodes",
  },
  {
    title: "Monitoring",
    slug: "operator/monitoring",
    icon: Activity,
    description: "Cluster health and alerts",
  },
  {
    title: "Upgrades",
    slug: "operator/upgrades",
    icon: ArrowUpCircle,
    description: "Rolling upgrade protocol",
  },
  {
    title: "WireGuard",
    slug: "operator/wireguard",
    icon: Shield,
    description: "Mesh network configuration",
  },
  {
    title: "Security",
    slug: "operator/security",
    icon: Shield,
    description: "Network security and hardening",
  },
  {
    title: "OramaOS",
    slug: "operator/orama-os",
    icon: HardDrive,
    description: "Locked-down node operating system",
  },
  {
    title: "Troubleshooting",
    slug: "operator/troubleshooting",
    icon: Wrench,
    description: "Common issues and fixes",
  },
  {
    title: "Nameserver",
    slug: "operator/nameserver",
    icon: Globe,
    description: "DNS and nameserver config",
  },
  {
    title: "Video Tutorials",
    slug: "operator/video-tutorials",
    icon: MonitorPlay,
    description: "Step-by-step video guides",
  },
];

export const CONTRIBUTOR_DOCS: DocLink[] = [
  {
    title: "Architecture",
    slug: "contributor/architecture",
    icon: LayoutDashboard,
    description: "System design overview",
  },
  {
    title: "Dev Setup",
    slug: "contributor/dev-setup",
    icon: Laptop,
    description: "Local development environment",
  },
  {
    title: "Code Style",
    slug: "contributor/code-style",
    icon: FileText,
    description: "Coding conventions",
  },
  {
    title: "Testing",
    slug: "contributor/testing",
    icon: FlaskConical,
    description: "Test strategy and tooling",
  },
  {
    title: "Deployment",
    slug: "contributor/deployment",
    icon: Upload,
    description: "CI/CD and release process",
  },
  {
    title: "Video Tutorials",
    slug: "contributor/video-tutorials",
    icon: MonitorPlay,
    description: "Step-by-step video guides",
  },
];

/** Lookup table: persona → flat doc list */
export const PERSONA_DOCS: Record<Persona, DocLink[]> = {
  developer: DEVELOPER_DOCS,
  operator: OPERATOR_DOCS,
  contributor: CONTRIBUTOR_DOCS,
};

/** All docs across all personas (for search) */
export const ALL_DOCS: { link: DocLink; persona: Persona }[] = [
  ...DEVELOPER_DOCS.map((link) => ({ link, persona: "developer" as Persona })),
  ...OPERATOR_DOCS.map((link) => ({ link, persona: "operator" as Persona })),
  ...CONTRIBUTOR_DOCS.map((link) => ({
    link,
    persona: "contributor" as Persona,
  })),
];

/** First slug for each persona — used for persona switching navigation */
export const PERSONA_FIRST_SLUG: Record<Persona, string> = {
  developer: "developer/getting-started",
  operator: "operator/getting-started",
  contributor: "contributor/architecture",
};

/* Compat: DOCS_SECTIONS still used by docs.tsx for SLUG_TITLE_MAP */
export interface DocSection {
  title: string;
  persona: Persona;
  links: DocLink[];
}

export const DOCS_SECTIONS: DocSection[] = [
  { title: "Developer", persona: "developer", links: DEVELOPER_DOCS },
  { title: "Operator", persona: "operator", links: OPERATOR_DOCS },
  { title: "Contributor", persona: "contributor", links: CONTRIBUTOR_DOCS },
];
