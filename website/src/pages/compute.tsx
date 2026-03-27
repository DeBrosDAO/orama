import { lazy, Suspense, useState } from "react";
import { Link } from "react-router";
import { Page } from "../components/layout/page";
import { SplitText } from "../components/ui/split-text";

const OramaOneInline = lazy(() =>
  import("../components/landing/orama-one-scene").then((m) => ({
    default: m.OramaOneInline,
  })),
);

const ComputeMeshScene = lazy(() =>
  import("../components/landing/compute-mesh-scene").then((m) => ({
    default: m.ComputeMeshScene,
  })),
);
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { Button } from "../components/ui/button";
import { DashedPanel } from "../components/ui/dashed-panel";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";
import { StatusDot } from "../components/ui/status-dot";
import { SILVER } from "../components/ui/silver-theme";
import {
  ArrowRight,
  ExternalLink,
  Globe,
  Server,
  Database,
  HardDrive,
  Zap,
  Brain,
  Shield,
  Code,
  Terminal,
  LayoutDashboard,
  ChevronRight,
  Check,
  X,
  Gift,
} from "lucide-react";

/* ═══════════════════════════════════════════
   1. HERO
   ═══════════════════════════════════════════ */
function ComputeHero() {
  return (
    <Section padding="wide">
      <div className="flex flex-col items-center text-center min-h-[70vh] pt-[12vh] gap-6 max-w-3xl mx-auto">
        <span
          className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full"
          style={{ border: `1px dashed ${SILVER.mid}`, color: SILVER.light }}
        >
          DECENTRALIZED COMPUTE
        </span>

        <h1 className="font-display font-bold text-4xl lg:text-6xl leading-tight">
          <SplitText
            text="Deploy anywhere."
            className="text-fg"
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
          />
          <br />
          <SplitText
            text="Own everything."
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
            className=""
          />
        </h1>

        <style>{`
          h1 > span:last-of-type .split-char {
            background: ${SILVER.gradient};
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
          }
        `}</style>

        <p className="text-muted max-w-lg text-sm leading-relaxed">
          A decentralized cloud powered by community-owned hardware.
          Deploy websites, APIs, and databases — running on Orama nodes,
          not corporate data centers.
        </p>

        <div className="flex flex-wrap items-center justify-center gap-3">
          <Link to="/investors">
            <Button size="lg" className="silver-button text-black font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm cursor-pointer opacity-50 pointer-events-none">
              COMING SOON <ArrowRight className="w-4 h-4 ml-2" />
            </Button>
          </Link>
          <a href="https://t.me/debrosportal" target="_blank" rel="noopener noreferrer">
            <Button size="lg" variant="dashed" className="font-mono tracking-wider uppercase">
              JOIN WAITLIST <ArrowRight className="w-4 h-4 ml-2" />
            </Button>
          </a>
        </div>

        <div className="flex items-center gap-2 text-xs font-mono text-muted">
          <StatusDot status="active" />
          <span>TESTNET LIVE — 300 NODES REQUIRED FOR GENESIS</span>
        </div>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   2. WHAT YOU CAN DEPLOY
   ═══════════════════════════════════════════ */
function WhatYouCanDeploy() {
  const deployables = [
    { icon: Globe, title: "Static Sites", desc: "React, Next.js, Vue — deploy any frontend to the decentralized edge." },
    { icon: Server, title: "APIs & Backends", desc: "Go, Node, Python — run full backend services across distributed nodes." },
    { icon: Database, title: "Databases", desc: "Distributed databases with automatic replication and fault tolerance." },
    { icon: HardDrive, title: "Storage", desc: "Decentralized file storage with IPFS integration. Permanent, censorship-resistant." },
    { icon: Zap, title: "Serverless Functions", desc: "WASM-powered edge functions. Execute code at the closest node to your users." },
    { icon: Brain, title: "AI Marketplace", desc: "Native AI Marketplace with autonomous AI agents (Angels) that interact with on-chain primitives." },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="What You Can Deploy"
          subtitle="Everything you'd deploy on AWS or Vercel — but decentralized, private, and community-owned."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 mt-8">
        {deployables.map((item) => (
          <AnimateIn key={item.title}>
            <DashedPanel withBackground className="h-full">
              <div className="flex flex-col gap-3 p-5">
                <item.icon className="w-5 h-5" style={{ color: SILVER.light }} />
                <span className="font-display font-bold text-sm text-fg">{item.title}</span>
                <span className="text-xs text-muted leading-relaxed">{item.desc}</span>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   3. ORAMA ONE — HARDWARE NODE
   ═══════════════════════════════════════════ */
function OramaOneSection() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Orama One"
          subtitle="The heart of the compute layer. A purpose-built hardware node that powers decentralized infrastructure."
        />
      </AnimateIn>

      <AnimateIn>
        <DashedPanel withCorners withBackground className="mt-8">
          <div className="flex flex-col items-center text-center gap-6 py-12 px-4 pb-0">
            <span
              className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full"
              style={{ border: `1px dashed ${SILVER.mid}`, color: SILVER.mid }}
            >
              HARDWARE NODE
            </span>

            <p className="text-muted text-sm max-w-lg leading-relaxed">
              Plug in. Connect. Start powering the decentralized cloud.
              Every Orama One node runs OramaOS — hardened, read-only, TPM-attested —
              earning $ORAMA with a 1.5x Infrastructure Multiplier.
              3D-printed, open-source hardware design. 4+ cores, 8GB RAM, 256GB NVMe.
            </p>

            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 w-full max-w-lg">
              {[
                { label: "Consensus", value: "Hybrid PoS+PoC+PoI" },
                { label: "Block Time", value: "6 seconds" },
                { label: "OramaOS", value: "1.5x Multiplier" },
                { label: "Status", value: "Coming Soon" },
              ].map((stat) => (
                <div key={stat.label} className="flex flex-col gap-1 text-center">
                  <span className="text-xs font-mono text-zinc-500 uppercase">{stat.label}</span>
                  <span className="text-sm font-bold" style={{ color: SILVER.light }}>{stat.value}</span>
                </div>
              ))}
            </div>

            <Button className="silver-button text-black font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm opacity-50 pointer-events-none">
              Coming Soon <ArrowRight className="w-4 h-4 ml-2" />
            </Button>

            {/* 3D Node */}
            <div className="relative w-full" style={{ height: 280 }}>
              <Suspense fallback={<div className="w-full h-full" />}>
                <OramaOneInline />
              </Suspense>
            </div>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   4. RUN A NODE
   ═══════════════════════════════════════════ */
function RunANode() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Run a Node"
          subtitle="No licenses. No gatekeeping. Run a node on testnet for free — stake 1,000 $ORAMA at mainnet."
        />
      </AnimateIn>

      {/* DeBros NFT Alert */}
      <AnimateIn>
        <div
          className="flex items-start gap-3 rounded-sm p-4 mt-8 border"
          style={{ borderColor: "rgba(168, 85, 247, 0.3)", background: "rgba(168, 85, 247, 0.05)" }}
        >
          <Gift className="w-5 h-5 text-purple-400 mt-0.5 shrink-0" />
          <div>
            <span className="text-sm font-semibold text-purple-300">DeBros Team NFT Holders</span>
            <p className="text-xs text-purple-400/80 mt-1">
              100 Team NFTs: <strong>40% governance power</strong> (5 votes each) +{" "}
              <strong>50% of BTC bridge fees</strong> auto-swapped to $ORAMA every epoch.{" "}
              <a
                href="https://debros.io/nft"
                target="_blank"
                rel="noopener noreferrer"
                className="underline hover:text-purple-300"
              >
                View the collection &rarr;
              </a>
            </p>
          </div>
        </div>
      </AnimateIn>

      {/* Timeline */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-6">
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-4 p-6">
              <div className="flex items-center gap-2">
                <span
                  className="w-8 h-8 flex items-center justify-center rounded-full text-xs font-mono font-bold text-black"
                  style={{ background: SILVER.gradient }}
                >
                  1
                </span>
                <span className="font-display font-bold text-fg">Testnet — Free to Join</span>
              </div>
              <p className="text-xs text-muted leading-relaxed">
                No staking required. Run a node and start earning $ORAMA block rewards immediately.
                Testnet tokens are real — they carry over to mainnet. Earn 3,840 $ORAMA/day per node
                at 300 nodes in Era 1.
              </p>
              <div className="flex flex-wrap gap-2">
                {["Zero Stake", "Real Tokens", "Carry Over to Mainnet"].map((tag) => (
                  <span
                    key={tag}
                    className="text-[10px] font-mono px-2 py-0.5 rounded-sm"
                    style={{ border: `1px solid ${SILVER.border}`, color: SILVER.mid }}
                  >
                    {tag}
                  </span>
                ))}
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>

        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-4 p-6">
              <div className="flex items-center gap-2">
                <span
                  className="w-8 h-8 flex items-center justify-center rounded-full text-xs font-mono font-bold text-black"
                  style={{ background: SILVER.gradient }}
                >
                  2
                </span>
                <span className="font-display font-bold text-fg">Mainnet — 300 Nodes Verified</span>
              </div>
              <p className="text-xs text-muted leading-relaxed">
                Full production launch when 300 independent nodes are running and verified.
                Staking activates at 1,000 $ORAMA. BTC bridge live. Native DEX live.
                Every testnet node runner will have earned more than enough to stake.
              </p>
              <div className="flex flex-wrap gap-2">
                {["1,000 $ORAMA Stake", "BTC Bridge", "Native DEX"].map((tag) => (
                  <span
                    key={tag}
                    className="text-[10px] font-mono px-2 py-0.5 rounded-sm"
                    style={{ border: `1px solid ${SILVER.border}`, color: SILVER.mid }}
                  >
                    {tag}
                  </span>
                ))}
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>

      {/* Hardware Specs */}
      <AnimateIn>
        <DashedPanel withCorners withBackground className="mt-6">
          <div className="flex flex-col gap-4 p-6">
            <span className="font-display font-bold text-lg text-fg">Hardware Requirements</span>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div className="flex flex-col gap-2">
                <span className="text-xs font-mono text-muted uppercase tracking-wider">Standard Node</span>
                <p className="text-xs text-muted">4+ cores, 8GB RAM, 256GB NVMe, 1Gbps, TPM 2.0</p>
              </div>
              <div className="flex flex-col gap-2">
                <span className="text-xs font-mono text-muted uppercase tracking-wider">Cloud Node (min)</span>
                <p className="text-xs text-muted">2+ vCPU, 4GB RAM, 80GB SSD, OramaOS image</p>
              </div>
            </div>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   5. HOW COMPUTE WORKS
   ═══════════════════════════════════════════ */
function HowComputeWorks() {
  const steps = [
    {
      num: "01",
      title: "Deploy",
      desc: "Push your code via the Orama CLI or dashboard. Static sites, APIs, databases — anything.",
      icon: Terminal,
    },
    {
      num: "02",
      title: "Distribute",
      desc: "Your application is compiled to pure WASM and distributed across Orama nodes. BFT consensus ensures fault tolerance with 6-second block times.",
      icon: Server,
    },
    {
      num: "03",
      title: "Serve",
      desc: "Users hit the closest node. Per-transaction privacy via PLONK zk-SNARKs. No single point of failure. Censorship-resistant by design.",
      icon: Globe,
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="How It Works"
          subtitle="From code to production in three steps. No vendor lock-in. No corporate middleman."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-8">
        {steps.map((step, i) => (
          <AnimateIn key={step.num}>
            <DashedPanel withBackground className="h-full">
              <div className="flex flex-col gap-4 p-5">
                <div className="flex items-center gap-3">
                  <span
                    className="text-xs font-mono font-bold"
                    style={{ color: SILVER.mid }}
                  >
                    {step.num}
                  </span>
                  {i < steps.length - 1 && (
                    <ChevronRight className="w-3 h-3 text-zinc-600 hidden md:block ml-auto" />
                  )}
                </div>
                <step.icon className="w-5 h-5" style={{ color: SILVER.light }} />
                <span className="font-display font-bold text-fg">{step.title}</span>
                <span className="text-xs text-muted leading-relaxed">{step.desc}</span>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   6. WHY NOT AWS / VERCEL?
   ═══════════════════════════════════════════ */
function WhyDecentralized() {
  const comparisons = [
    { feature: "Ownership", centralized: "Their servers, their rules", orama: "Community-owned hardware" },
    { feature: "Privacy", centralized: "They see everything", orama: "Zero-knowledge by default" },
    { feature: "Censorship", centralized: "Can shut you down", orama: "No single point of control" },
    { feature: "Vendor Lock-in", centralized: "Proprietary APIs & formats", orama: "Open source, standard APIs" },
    { feature: "Cost", centralized: "Pay for their margins", orama: "Pay node operators directly" },
    { feature: "Earn", centralized: "You can't", orama: "Run a node, earn $ORAMA" },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Why Not AWS?"
          subtitle="Centralized clouds are convenient. But convenience has a cost."
        />
      </AnimateIn>

      <AnimateIn>
        <DashedPanel withCorners withBackground className="mt-8 overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full text-xs sm:text-sm min-w-[500px]">
              <thead>
                <tr className="border-b" style={{ borderColor: SILVER.border }}>
                  <th className="text-left p-2 sm:p-4 font-mono text-[10px] sm:text-xs text-zinc-500 uppercase tracking-wider w-1/3">Feature</th>
                  <th className="text-left p-2 sm:p-4 font-mono text-[10px] sm:text-xs text-zinc-500 uppercase tracking-wider w-1/3">Centralized Cloud</th>
                  <th className="text-left p-2 sm:p-4 font-mono text-[10px] sm:text-xs text-zinc-500 uppercase tracking-wider w-1/3">Orama Compute</th>
                </tr>
              </thead>
              <tbody>
                {comparisons.map((row, i) => (
                  <tr
                    key={row.feature}
                    className={i < comparisons.length - 1 ? "border-b" : ""}
                    style={{ borderColor: SILVER.border }}
                  >
                    <td className="p-4 font-display font-bold text-fg text-xs">{row.feature}</td>
                    <td className="p-4 text-xs text-zinc-500">
                      <span className="flex items-center gap-2">
                        <X className="w-3 h-3 text-red-400/60 shrink-0" />
                        {row.centralized}
                      </span>
                    </td>
                    <td className="p-4 text-xs" style={{ color: SILVER.light }}>
                      <span className="flex items-center gap-2">
                        <Check className="w-3 h-3 text-emerald-400/60 shrink-0" />
                        {row.orama}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   7. DEVELOPER TOOLS
   ═══════════════════════════════════════════ */
function DeveloperTools() {
  const tools = [
    {
      icon: Terminal,
      title: "Orama CLI",
      desc: "Deploy from your terminal. One command to go from local to decentralized.",
      link: "https://github.com/DeBrosDAO/orama",
      linkText: "View on GitHub",
    },
    {
      icon: Code,
      title: "Network SDK",
      desc: "TypeScript SDK for interacting with the Orama Network. Build dApps, manage deployments.",
      link: "https://github.com/DeBrosDAO/network-ts-sdk",
      linkText: "View on GitHub",
    },
    {
      icon: LayoutDashboard,
      title: "Dashboard",
      desc: "Web interface for managing deployments, monitoring nodes, and viewing analytics.",
      link: "/dashboard",
      linkText: "Open Dashboard",
      internal: true,
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Developer Tools"
          subtitle="Everything is open source. Build on Orama with tools you already know."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-8">
        {tools.map((tool) => (
          <AnimateIn key={tool.title}>
            <DashedPanel withBackground className="h-full">
              <div className="flex flex-col gap-3 p-5 h-full">
                <tool.icon className="w-5 h-5" style={{ color: SILVER.light }} />
                <span className="font-display font-bold text-sm text-fg">{tool.title}</span>
                <span className="text-xs text-muted leading-relaxed flex-1">{tool.desc}</span>
                {tool.internal ? (
                  <Link to={tool.link}>
                    <Button variant="ghost" size="sm" className="text-xs font-mono mt-auto">
                      {tool.linkText} <ChevronRight className="w-3 h-3 ml-1" />
                    </Button>
                  </Link>
                ) : (
                  <a href={tool.link} target="_blank" rel="noopener noreferrer">
                    <Button variant="ghost" size="sm" className="text-xs font-mono mt-auto">
                      {tool.linkText} <ExternalLink className="w-3 h-3 ml-1" />
                    </Button>
                  </a>
                )}
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>

      {/* Orama Vault + RootWallet */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-4">
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col gap-3 p-5">
              <Shield className="w-5 h-5" style={{ color: SILVER.light }} />
              <span className="font-display font-bold text-sm text-fg">Orama Vault</span>
              <span className="text-xs text-muted leading-relaxed">
                Decentralized secrets management. Store API keys, credentials, and
                environment variables securely across the network.
              </span>
              <a href="https://github.com/DeBrosDAO/orama-vault" target="_blank" rel="noopener noreferrer">
                <Button variant="ghost" size="sm" className="text-xs font-mono">
                  View on GitHub <ExternalLink className="w-3 h-3 ml-1" />
                </Button>
              </a>
            </div>
          </DashedPanel>
        </AnimateIn>

        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col gap-3 p-5">
              <Zap className="w-5 h-5" style={{ color: SILVER.light }} />
              <span className="font-display font-bold text-sm text-fg">RootWallet</span>
              <span className="text-xs text-muted leading-relaxed">
                One wallet for everything. Manage identities, sign transactions,
                and interact with Orama services. Open source soon.
              </span>
              <a href="https://rootwallet.io" target="_blank" rel="noopener noreferrer">
                <Button variant="ghost" size="sm" className="text-xs font-mono">
                  rootwallet.io <ExternalLink className="w-3 h-3 ml-1" />
                </Button>
              </a>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   8. FAQ
   ═══════════════════════════════════════════ */
function ComputeFAQ() {
  const [openIndex, setOpenIndex] = useState<number | null>(null);
  const faqs = [
    {
      question: "What can I deploy on Orama Compute?",
      answer: "Static sites, APIs, backends (Go, Node, Python), databases, file storage via IPFS, serverless WASM functions, and AI workloads via the native AI Marketplace. Write smart contracts in any language that compiles to WebAssembly.",
    },
    {
      question: "How do I run a node?",
      answer: "During testnet, anyone can run a node with zero staking. Just get a VPS (2+ vCPU, 4GB RAM, 80GB SSD) or dedicated hardware (4+ cores, 8GB RAM, 256GB NVMe), install OramaOS, and start earning. Testnet tokens carry over to mainnet.",
    },
    {
      question: "What is the staking requirement?",
      answer: "On testnet: zero. At mainnet launch, the minimum stake is 1,000 $ORAMA. Every testnet node runner will have earned more than enough to stake by then.",
    },
    {
      question: "What happens if my node goes down?",
      answer: "Slashing is progressive: downtime over 20% results in a 5-30% slash. Double-signing is a 100% slash, and false infrastructure attestation is 50%. Slashed tokens are burned permanently.",
    },
    {
      question: "How does the consensus mechanism work?",
      answer: "Orama uses hybrid PoS + Proof of Contribution + Proof of Infrastructure. Effective Power = Stake x (1 + Contribution Score) x Infrastructure Multiplier. OramaOS nodes get a 1.5x multiplier. Contribution is weighted: 40% uptime, 30% bandwidth, 20% compute, 10% reliability.",
    },
    {
      question: "How is this different from Akash or Filecoin?",
      answer: "Orama is a standalone L1 blockchain with compute as a native primitive — not a separate marketplace. BTC-only economy, native DEX, PLONK privacy, AI Marketplace, and on-chain governance all live on the same chain. No bridging between protocols.",
    },
    {
      question: "What payments are accepted?",
      answer: "Only BTC and $ORAMA. Gas is always paid in $ORAMA. Base fee is burned. This is a BTC-only economy by design — no stablecoins, no altcoins.",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader title="FAQ" subtitle="Common questions about Orama Compute." />
      </AnimateIn>

      <div className="flex flex-col gap-2 mt-8 max-w-2xl mx-auto">
        {faqs.map((faq, i) => (
          <AnimateIn key={i}>
            <DashedPanel withBackground>
              <button
                className="w-full text-left p-4 flex items-start justify-between gap-4 cursor-pointer"
                onClick={() => setOpenIndex(openIndex === i ? null : i)}
              >
                <span className="font-display font-bold text-sm text-fg">{faq.question}</span>
                <ChevronRight
                  className="w-4 h-4 text-muted shrink-0 mt-0.5 transition-transform"
                  style={{ transform: openIndex === i ? "rotate(90deg)" : undefined }}
                />
              </button>
              {openIndex === i && (
                <div className="px-4 pb-4">
                  <p className="text-xs text-muted leading-relaxed">{faq.answer}</p>
                </div>
              )}
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   9. CTA
   ═══════════════════════════════════════════ */
function ComputeCTA() {
  return (
    <Section padding="wide">
      <AnimateIn>
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col items-center text-center gap-6 py-16 px-4">
            <h2 className="font-display font-bold text-2xl lg:text-4xl text-fg">
              Ready to own your infrastructure?
            </h2>
            <p className="text-muted text-sm max-w-md">
              Run a node on testnet for free — no staking required. Tokens carry
              over to mainnet. Or join the developer waitlist for early access.
            </p>
            <div className="flex flex-wrap items-center justify-center gap-3">
              <Button className="silver-button text-black font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm opacity-50 pointer-events-none">
                COMING SOON <ArrowRight className="w-4 h-4 ml-2" />
              </Button>
              <a href="https://t.me/debrosportal" target="_blank" rel="noopener noreferrer">
                <Button variant="dashed" className="font-mono tracking-wider uppercase px-8 py-3 text-sm">
                  JOIN WAITLIST <ArrowRight className="w-4 h-4 ml-2" />
                </Button>
              </a>
            </div>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   PAGE
   ═══════════════════════════════════════════ */
export default function Compute() {
  return (
    <Page title="Orama Compute — Decentralized Cloud Infrastructure">
      <ComputeHero />
      <Suspense fallback={null}>
        <ComputeMeshScene />
      </Suspense>
      <CrosshairDivider />
      <WhatYouCanDeploy />
      <CrosshairDivider />
      <OramaOneSection />
      <CrosshairDivider />
      <RunANode />
      <CrosshairDivider />
      <HowComputeWorks />
      <CrosshairDivider />
      <WhyDecentralized />
      <CrosshairDivider />
      <DeveloperTools />
      <CrosshairDivider />
      <ComputeFAQ />
      <CrosshairDivider />
      <ComputeCTA />
    </Page>
  );
}
