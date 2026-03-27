import { useState, useEffect, useCallback, lazy, Suspense } from "react";
import { Link } from "react-router";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { DashedPanel } from "../components/ui/dashed-panel";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";
import { StatusDot } from "../components/ui/status-dot";
import { SplitText } from "../components/ui/split-text";
import { SILVER } from "../components/ui/silver-theme";
import { SponsorsShowcase } from "../components/landing/sponsors-showcase";
import {
  ArrowRight,
  ExternalLink,
  Server,
  Users,
  Code,
  Shield,
  Lock,
  EyeOff,
  Wallet,
  ChevronRight,
  Database,
  Cpu,
  Globe,
  Layers,
  Coins,
  Vote,
  Repeat,
  HardDrive,
} from "lucide-react";
import debrosIcon from "../assets/debrosnet.png";

const NetworkVisualization = lazy(() =>
  import("../components/landing/network-visualization").then((m) => ({
    default: m.NetworkVisualization,
  })),
);

const OramaOneScene = lazy(() =>
  import("../components/landing/orama-one-scene").then((m) => ({
    default: m.OramaOneScene,
  })),
);

const OramaOneInline = lazy(() =>
  import("../components/landing/orama-one-scene").then((m) => ({
    default: m.OramaOneInline,
  })),
);

/* ═══════════════════════════════════════════
   1. HERO
   ═══════════════════════════════════════════ */
function HomeHero() {
  return (
    <div className="relative py-24 sm:py-32">
      {/* 3D Orama One node — full viewport width, pinned to bottom */}
      <Suspense fallback={null}>
        <OramaOneScene />
      </Suspense>

      <div className="relative flex flex-col items-center text-center min-h-[70vh] justify-center gap-6 max-w-3xl mx-auto px-4">

        <span
          className="relative z-10 inline-flex items-center px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full"
          style={{
            border: `1px dashed ${SILVER.mid}`,
            color: SILVER.light,
          }}
        >
          Decentralized Cloud + L1 Blockchain
        </span>

        <h1 className="relative z-10 font-display font-bold text-4xl lg:text-6xl leading-tight">
          <SplitText
            text="Blockchain was step one."
            className="text-fg"
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
          />
          <br />
          <SplitText
            text="Orama is step two."
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
            className=""
          />
        </h1>

        {/* Gradient shimmer on the second line */}
        <style>{`
          h1 > span:last-of-type .split-char {
            background: ${SILVER.gradient};
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
          }
        `}</style>

        <p className="relative z-10 text-muted text-sm leading-relaxed max-w-lg">
          Bitcoin gave us decentralized money. Ethereum gave us decentralized
          contracts. Orama gives us decentralized everything —
          an L1 blockchain fused with a full cloud platform.
        </p>

        <div className="relative z-10 flex flex-wrap gap-2 justify-center">
          {["L1 Blockchain", "Hardware Node", "Decentralized Compute", "OpenSource"].map((label) => (
            <span
              key={label}
              className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-wider rounded-full"
              style={{
                border: `1px solid ${SILVER.border}`,
                color: SILVER.mid,
                background: SILVER.bg,
              }}
            >
              {label}
            </span>
          ))}
        </div>

        <div className="relative z-10 flex flex-wrap items-center gap-3 justify-center pt-4">
          <a
            href="https://t.me/debrosportal"
            target="_blank"
            rel="noopener noreferrer"
            className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm cursor-pointer text-black"
          >
            Join the Waitlist
            <ArrowRight className="w-3.5 h-3.5 ml-2" />
          </a>
          <Button asChild variant="ghost" size="lg">
            <Link to="/investors">
              Become an Investor
              <ArrowRight className="w-3.5 h-3.5 ml-2" />
            </Link>
          </Button>
        </div>

        <div className="relative z-10 flex items-center gap-2 mt-2">
          <span className="w-2.5 h-2.5 rounded-full bg-emerald-400 animate-pulse-dot" />
          <span className="text-xs font-mono text-muted tracking-wider uppercase">
            Testnet Live — 300 Nodes Required for Genesis
          </span>
        </div>

        <a
          href="https://debros.io"
          target="_blank"
          rel="noopener noreferrer"
          className="relative z-10 flex items-center gap-2 mt-4 text-muted/40 hover:text-muted transition-colors group"
        >
          <span className="text-xs font-mono tracking-wider">Built by</span>
          <span className="text-xs font-mono tracking-wider text-fg/60 group-hover:text-fg transition-colors">DeBros</span>
          <ExternalLink className="w-2.5 h-2.5" />
        </a>
      </div>
    </div>
  );
}

/* ═══════════════════════════════════════════
   2. BECOME AN INVESTOR (expanded)
   ═══════════════════════════════════════════ */
function InvestorSection() {
  return (
    <Section>
      <AnimateIn>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-8 items-center">
          {/* Left — pitch */}
          <div className="flex flex-col gap-6">
            <div className="flex items-center gap-3">
              <Wallet className="w-5 h-5 text-accent" />
              <span className="text-xs font-mono text-accent tracking-wider uppercase">
                For Investors
              </span>
            </div>

            <h2 className="font-display font-bold text-3xl text-fg">
              Back the infrastructure layer
              <br />
              <span
                style={{
                  background: SILVER.gradient,
                  WebkitBackgroundClip: "text",
                  WebkitTextFillColor: "transparent",
                }}
              >
                that replaces AWS.
              </span>
            </h2>

            <p className="text-muted leading-relaxed text-sm">
              210M $ORAMA — zero pre-mine, 100% mined. Era 1 block reward is 100 $ORAMA
              with Bitcoin-style halving every 2 years. BTC-only economy. No ETH, no SOL,
              no stablecoins. Run a node, earn tokens — the only way to get $ORAMA.
            </p>

            <div className="flex flex-wrap gap-3">
              <span className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-6 py-2.5 text-xs rounded-sm cursor-pointer text-black">
                <Link to="/investors" className="flex items-center">
                  Become an Investor
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </Link>
              </span>
              <Button asChild variant="ghost" size="default">
                <a href="/orama-whitepaper-v3.pdf" target="_blank" rel="noopener noreferrer">
                  Read Whitepaper
                  <ExternalLink className="w-3.5 h-3.5 ml-2" />
                </a>
              </Button>
            </div>
          </div>

          {/* Right — two investment paths */}
          <div className="flex flex-col gap-4">
            {/* Node licenses */}
            <DashedPanel withCorners withBackground>
              <div className="flex flex-col gap-3 p-3">
                <div className="flex items-center justify-between">
                  <div>
                    <span className="text-xs font-mono text-muted tracking-wider uppercase block">Node Licenses</span>
                    <span className="font-display font-bold text-fg">Run a node, earn $ORAMA</span>
                  </div>
                </div>
                <p className="text-xs text-muted">
                  Operate an Orama node. Earn $ORAMA block rewards based on Effective Power
                  (Stake x Contribution x Infrastructure). OramaOS nodes get a 1.5x multiplier.
                </p>
                <div className="flex items-center justify-end mt-1">
                  <span className="inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-4 py-1.5 text-xs rounded-sm text-black opacity-50 pointer-events-none" style={{ background: SILVER.mid }}>
                    Coming Soon
                  </span>
                </div>
              </div>
            </DashedPanel>

            {/* Bonding curve */}
            <DashedPanel withCorners withBackground>
              <div className="flex flex-col gap-3 p-3">
                <div className="flex items-center justify-between">
                  <div>
                    <span className="text-xs font-mono text-muted tracking-wider uppercase block">Protocol Bonding Curve</span>
                    <span className="font-display font-bold text-fg">Buy $ORAMA with BTC</span>
                  </div>
                </div>
                <p className="text-xs text-muted">
                  20% of block rewards flow into the bonding curve (capped at 21M $ORAMA).
                  Bridge BTC onto Orama and purchase $ORAMA at a mathematically determined price.
                  Early buyers pay less — the price rises with demand.
                </p>
                <div className="flex items-center justify-end mt-1">
                  <span className="inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-4 py-1.5 text-xs rounded-sm text-black opacity-50 pointer-events-none" style={{ background: SILVER.mid }}>
                    Coming Soon
                  </span>
                </div>
              </div>
            </DashedPanel>
          </div>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   3. JOIN THE WAITLIST (expanded)
   ═══════════════════════════════════════════ */
function WhitelistSection() {
  const perks = [
    "Early access to the decentralized cloud platform",
    "Free compute credits at launch",
    "Priority support from the core team",
    "Shape the product — your feedback drives the roadmap",
  ];

  return (
    <Section>
      <AnimateIn>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-8 items-center">
          {/* Left — what you get */}
          <div className="order-2 md:order-1">
            <DashedPanel withCorners withBackground>
              <div className="flex flex-col gap-4 p-4">
                <h3 className="font-display font-bold text-fg">
                  What waitlist members get
                </h3>
                <ul className="flex flex-col gap-3">
                  {perks.map((perk) => (
                    <li key={perk} className="flex items-start gap-2">
                      <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                      <span className="text-sm text-muted">{perk}</span>
                    </li>
                  ))}
                </ul>
                <div className="flex items-center gap-2 mt-2">
                  <StatusDot status="active" />
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">
                    Waitlist is open
                  </span>
                </div>
              </div>
            </DashedPanel>
          </div>

          {/* Right — pitch */}
          <div className="flex flex-col gap-6 order-1 md:order-2">
            <div className="flex items-center gap-3">
              <Code className="w-5 h-5 text-accent" />
              <span className="text-xs font-mono text-accent tracking-wider uppercase">
                For Developers
              </span>
            </div>

            <h2 className="font-display font-bold text-3xl text-fg">
              Deploy to infrastructure
              <br />
              <span className="text-accent">no one owns.</span>
            </h2>

            <p className="text-muted leading-relaxed">
              Websites, APIs, databases, serverless functions — deploy everything
              with one command to a global network of independent nodes. No vendor
              lock-in. No surprise bills. Total privacy by default.
            </p>

            <div className="flex flex-wrap gap-3">
              <Button asChild variant="dashed" size="lg" className="w-fit">
                <a href="https://t.me/debrosportal" target="_blank" rel="noopener noreferrer">
                  Join the Waitlist
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </a>
              </Button>
              <Button asChild variant="ghost" size="lg" className="w-fit">
                <Link to="/blockchain">
                  Explore the Blockchain
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </Link>
              </Button>
            </div>
          </div>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   4. HOW IT WORKS — Interactive 3D
   ═══════════════════════════════════════════ */
function HowItWorks() {
  const [activeStep, setActiveStep] = useState(0);

  const steps = [
    {
      number: "01",
      icon: Server,
      title: "Nodes power the network",
      description:
        "Independent operators run nodes on servers worldwide. Each node contributes compute, storage, and bandwidth — and earns $ORAMA for it. OramaOS nodes get a 1.5x Infrastructure Multiplier.",
    },
    {
      number: "02",
      icon: Code,
      title: "Developers deploy apps",
      description:
        "With one command, developers ship websites, APIs, databases, and functions to the network. Write smart contracts in Rust, Go, TypeScript, or any WASM language.",
    },
    {
      number: "03",
      icon: Users,
      title: "Users use normal apps",
      description:
        "End users don't need to know it's decentralized. Apps look and feel like any website. AnChat is the first — a private messenger built entirely on Orama.",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="How It Works"
          subtitle="Three layers, zero jargon."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8 items-center">
        <AnimateIn>
          <Suspense
            fallback={
              <div className="w-full aspect-square max-w-[500px] mx-auto bg-surface-2/30 rounded-lg" />
            }
          >
            <NetworkVisualization step={activeStep} />
          </Suspense>
        </AnimateIn>

        <AnimateIn>
          <div className="flex flex-col gap-3">
            {steps.map((step, i) => (
              <button
                key={step.number}
                type="button"
                onClick={() => setActiveStep(i)}
                className={`flex gap-4 p-4 text-left rounded-sm border transition-all duration-300 cursor-pointer ${
                  i === activeStep
                    ? "border-accent/30 bg-accent/5"
                    : "border-border hover:border-border/80 hover:bg-surface-2/50"
                }`}
              >
                <div className="flex flex-col items-center gap-2 shrink-0 pt-0.5">
                  <span className={`text-xs font-mono tracking-wider ${i === activeStep ? "text-accent" : "text-muted/40"}`}>
                    {step.number}
                  </span>
                  <step.icon className={`w-5 h-5 ${i === activeStep ? "text-accent" : "text-muted/40"}`} />
                </div>
                <div className="flex flex-col gap-1.5">
                  <h3 className={`font-display font-bold ${i === activeStep ? "text-fg" : "text-muted"}`}>
                    {step.title}
                  </h3>
                  <p className={`text-sm leading-relaxed transition-all duration-300 ${
                    i === activeStep ? "text-muted max-h-20 opacity-100" : "max-h-0 opacity-0 overflow-hidden"
                  }`}>
                    {step.description}
                  </p>
                </div>
              </button>
            ))}
          </div>
        </AnimateIn>
      </div>

      <AnimateIn>
        <div className="flex flex-wrap items-center justify-center gap-3 mt-8">
          <Button asChild size="lg">
            <Link to="/blockchain">
              Explore the Blockchain
              <ArrowRight className="w-3.5 h-3.5 ml-2" />
            </Link>
          </Button>
          <Button asChild variant="ghost" size="lg">
            <Link to="/compute">
              Explore Compute
              <ArrowRight className="w-3.5 h-3.5 ml-2" />
            </Link>
          </Button>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   5. LIVE ON ORAMA NETWORK (AnChat)
   ═══════════════════════════════════════════ */
const ANCHAT_SCREENS = Array.from({ length: 11 }, (_, i) => `/images/anchat-screens/${i + 1}.png`);

function AnChatShowcase() {
  const [current, setCurrent] = useState(0);

  const next = useCallback(() => {
    setCurrent((prev) => (prev + 1) % ANCHAT_SCREENS.length);
  }, []);

  useEffect(() => {
    const interval = setInterval(next, 3000);
    return () => clearInterval(interval);
  }, [next]);

  const cyan = "#00A5AF";

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Running on Orama Network"
          subtitle="The first app built entirely on decentralized infrastructure — currently in closed beta for DeBros NFT holders."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8 items-center">
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <div className="flex items-center gap-3">
              <span
                className="inline-flex items-center px-2.5 py-0.5 text-xs font-mono tracking-wider border border-dashed rounded-sm"
                style={{ borderColor: cyan, color: cyan }}
              >
                CLOSED BETA
              </span>
              <span className="text-xs font-mono text-muted tracking-wider uppercase">
                DeBros NFT Holders Only
              </span>
            </div>

            <div className="flex items-center gap-3">
              <img src="/images/anchat.png" alt="AnChat" className="w-10 h-10 rounded-lg" />
              <h3 className="font-display font-bold text-3xl" style={{ color: cyan }}>
                AnChat Lite
              </h3>
            </div>

            <p className="text-lg text-fg font-display">
              The Messenger They Cannot Track
            </p>

            <p className="text-muted leading-relaxed">
              A fully decentralized, private messenger running on the Orama Network
              and powered by the Orama Proxy. Unlike WhatsApp or Telegram, AnChat
              collects zero metadata — no phone number, no email, no IP logging.
              Connect with your crypto wallet and start chatting.
            </p>

            <div className="flex flex-wrap gap-2">
              <span className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono rounded-full" style={{ border: `1px solid ${cyan}30`, color: cyan, background: `${cyan}08` }}>
                <Lock className="w-3 h-3" /> E2E Encrypted
              </span>
              <span className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono rounded-full" style={{ border: `1px solid ${cyan}30`, color: cyan, background: `${cyan}08` }}>
                <Shield className="w-3 h-3" /> Wallet Login
              </span>
              <span className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono rounded-full" style={{ border: `1px solid ${cyan}30`, color: cyan, background: `${cyan}08` }}>
                <EyeOff className="w-3 h-3" /> Zero Metadata
              </span>
            </div>

            <div className="flex flex-wrap gap-3">
              <a
                href="https://anchat.io"
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm cursor-pointer transition-all duration-200"
                style={{ background: cyan, color: "#000" }}
              >
                Visit AnChat
                <ExternalLink className="w-3.5 h-3.5 ml-2" />
              </a>
            </div>
          </div>
        </AnimateIn>

        <AnimateIn>
          <div className="flex flex-col items-center gap-4">
            <div className="relative w-[min(300px,85vw)] h-[560px] sm:h-[600px] mx-auto">
              <div className="absolute inset-0 rounded-[2rem] overflow-hidden">
                {ANCHAT_SCREENS.map((src, i) => (
                  <img
                    key={src}
                    src={src}
                    alt={`AnChat screen ${i + 1}`}
                    className="absolute inset-0 w-full h-full object-cover transition-opacity duration-500"
                    style={{ opacity: i === current ? 1 : 0 }}
                  />
                ))}
              </div>
            </div>
            <div className="flex gap-1.5">
              {ANCHAT_SCREENS.map((_, i) => (
                <button
                  key={i}
                  type="button"
                  onClick={() => setCurrent(i)}
                  className="w-1.5 h-1.5 rounded-full transition-all duration-300 cursor-pointer"
                  style={{
                    background: i === current ? cyan : "rgba(113,113,122,0.3)",
                    width: i === current ? "1rem" : undefined,
                  }}
                  aria-label={`Show screen ${i + 1}`}
                />
              ))}
            </div>
          </div>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   6. BLOCKCHAIN SECTION
   ═══════════════════════════════════════════ */
function BlockchainSection() {
  const features = [
    { icon: Shield, title: "Hybrid Consensus", description: "Proof of Stake + Proof of Contribution + Proof of Infrastructure. Effective Power = Stake x (1 + Contribution) x Infrastructure Multiplier." },
    { icon: Vote, title: "On-Chain Governance", description: "NFT holders (75% voting power) + token holders (25% quadratic). Three governance tiers: emergency (24h), protocol (3 days), constitutional (14 days)." },
    { icon: Wallet, title: "BTC-Only Economy", description: "Only two assets: BTC and $ORAMA. Native BTC bridge with 0.25% fee. Protocol-native order book for $ORAMA/BTC. No ETH, no SOL, no stablecoins." },
    { icon: Coins, title: "210M Hard Cap", description: "Zero pre-mine, 100% mined. Era 1 = 100 $ORAMA/block. Bitcoin-style halving every 2 years. 80% to miner, 20% to bonding curve." },
    { icon: Lock, title: "PLONK zk-SNARKs", description: "Per-transaction public/private toggle. Private mode hides sender, receiver, and amount. 4x gas for private transactions. Universal trusted setup." },
    { icon: Code, title: "Pure WASM VM", description: "Write smart contracts in Rust, Go, TypeScript, C++, or any WASM language. No EVM, no Solidity required." },
    { icon: Repeat, title: "Native DEX", description: "Protocol-native order book for $ORAMA/BTC. Not an AMM — pure price discovery. Randomized ordering prevents front-running." },
    { icon: Users, title: "AI Marketplace", description: "Host AI models and Angels (autonomous agents) on-chain. Compute providers capped at 10% of network nodes. Pay per use in $ORAMA." },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="L1 Blockchain"
          subtitle="A purpose-built blockchain for decentralized infrastructure and AI — not another EVM fork."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8 items-start">
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <p className="text-muted leading-relaxed">
              Most blockchains are financial ledgers. Orama's L1 is built for
              <span className="text-fg font-semibold"> infrastructure and AI</span> — Proof of Infrastructure
              rewards real work over staked capital, Proof of Stake provides economic
              security, and Proof of Contribution measures uptime, bandwidth, and compute every epoch.
            </p>
            <p className="text-muted leading-relaxed">
              Transactions can be private (PLONK zk-SNARKs) or public — your choice.
              AI agents transact natively. Developers write smart contracts in Rust, Go, TypeScript,
              or any language that compiles to WASM. Powered by <span className="text-fg font-semibold">RootWallet</span> for
              seamless wallet onboarding.
            </p>
            <p className="text-muted leading-relaxed">
              The $ORAMA token coordinates everything: operators stake to run nodes,
              developers pay for resources, AI agents transact autonomously, and
              on-chain governance decides how the network evolves. 6-second block time,
              1-hour epoch finality via BFT checkpoints.
            </p>
            <Button asChild variant="ghost" size="default" className="w-fit">
              <Link to="/blockchain">
                Explore the L1
                <ArrowRight className="w-3.5 h-3.5 ml-2" />
              </Link>
            </Button>
          </div>
        </AnimateIn>

        <AnimateIn>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            {features.map((f) => (
              <DashedPanel key={f.title} withBackground className="h-full">
                <div className="flex flex-col gap-3 p-2">
                  <f.icon className="w-5 h-5 text-accent" />
                  <h3 className="font-display font-bold text-sm text-fg">{f.title}</h3>
                  <p className="text-muted text-xs leading-relaxed">{f.description}</p>
                </div>
              </DashedPanel>
            ))}
          </div>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   7. DECENTRALIZED COMPUTE SECTION
   ═══════════════════════════════════════════ */
function ComputeSection() {
  const services = [
    { icon: Database, title: "Distributed SQL", description: "On-chain SQL database primitive with Raft consensus and automatic replication across the node mesh." },
    { icon: HardDrive, title: "IPFS + KV Storage", description: "Content-addressed IPFS storage and key-value store distributed across the network. 10,000 rays/MB (IPFS), 200 rays/op (KV)." },
    { icon: Cpu, title: "Serverless WASM", description: "Deploy WASM functions in Rust, Go, TypeScript, C++ — execute across the node mesh. 1,000 rays per 1M instructions." },
    { icon: Globe, title: "DNS", description: "Decentralized CoreDNS — your domains resolve through the network." },
    { icon: Layers, title: "In-Memory Cache", description: "Olric distributed cache with namespace isolation and auto-replication." },
    { icon: Users, title: "PubSub & WebRTC", description: "Real-time messaging and peer-to-peer communication built in." },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Decentralized Compute"
          subtitle="Everything you need to run production apps — without a cloud provider."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8 items-start">
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <p className="text-muted leading-relaxed">
              Orama isn't just a blockchain that happens to have infrastructure.
              <span className="text-fg font-semibold"> It's a full cloud platform</span> — databases, storage,
              compute, caching, DNS, and real-time messaging — distributed across
              a global network of independent nodes.
            </p>
            <p className="text-muted leading-relaxed">
              Developers deploy real applications with one command. Not smart contracts.
              Not toy demos. Production websites, APIs, and services — running on
              infrastructure that no single company can shut down.
            </p>
            <Button asChild variant="ghost" size="default" className="w-fit">
              <Link to="/developers">
                Explore Compute Platform
                <ArrowRight className="w-3.5 h-3.5 ml-2" />
              </Link>
            </Button>
          </div>
        </AnimateIn>

        <AnimateIn>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            {services.map((s) => (
              <DashedPanel key={s.title} withBackground className="h-full">
                <div className="flex flex-col gap-2 p-2">
                  <s.icon className="w-4 h-4 text-accent" />
                  <h3 className="font-display font-bold text-sm text-fg">{s.title}</h3>
                  <p className="text-muted text-xs leading-relaxed">{s.description}</p>
                </div>
              </DashedPanel>
            ))}
          </div>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   8. ORAMA ONE — Hardware Node
   ═══════════════════════════════════════════ */
function OramaOneSection() {
  const specs = [
    { label: "CPU", value: "4+ cores, 2.0+ GHz (ARM or x86)" },
    { label: "RAM", value: "8 GB" },
    { label: "Storage", value: "256 GB NVMe SSD" },
    { label: "Network", value: "1 Gbps Ethernet" },
    { label: "TPM", value: "TPM 2.0 for OramaOS attestation" },
    { label: "Power", value: "Under 25W continuous" },
  ];

  return (
    <Section>
      <AnimateIn>
        <DashedPanel withCorners withBackground className="!pb-0">
          <div className="flex flex-col items-center text-center gap-6 py-12 px-4 pb-0">
            <span
              className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full"
              style={{ border: `1px dashed ${SILVER.mid}`, color: SILVER.light }}
            >
              HARDWARE
            </span>

            <h2
              className="font-display font-bold text-3xl lg:text-4xl"
              style={{
                background: SILVER.gradient,
                WebkitBackgroundClip: "text",
                WebkitTextFillColor: "transparent",
              }}
            >
              Orama One
            </h2>

            <p className="text-muted text-sm leading-relaxed max-w-lg">
              The heart of the blockchain and the compute layer. A purpose-built, 3D-printed
              hardware node designed to power the Orama Network — plug in, connect,
              and start earning $ORAMA. Fanless, compact, and silent. Open-source hardware
              so anyone can build their own.
            </p>

            <div className="flex flex-wrap gap-2 justify-center">
              {["Plug & Earn", "Silent & Fanless", "Open-Source Hardware", "OramaOS 1.5x Multiplier"].map((label) => (
                <span
                  key={label}
                  className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-wider rounded-full"
                  style={{ border: `1px solid ${SILVER.border}`, color: SILVER.mid, background: SILVER.bg }}
                >
                  {label}
                </span>
              ))}
            </div>

            <div className="grid grid-cols-2 sm:grid-cols-3 gap-3 max-w-lg w-full mt-2">
              {specs.map((s) => (
                <div
                  key={s.label}
                  className="flex flex-col gap-1 p-3 rounded-sm text-left"
                  style={{ border: `1px solid ${SILVER.border}`, background: SILVER.bg }}
                >
                  <span className="text-[10px] font-mono tracking-wider uppercase" style={{ color: SILVER.mid }}>
                    {s.label}
                  </span>
                  <span className="text-xs font-mono text-fg">{s.value}</span>
                </div>
              ))}
            </div>

            <p className="text-xs font-mono text-muted/50 tracking-wider uppercase mt-4">
              Coming soon — details to be announced
            </p>

            <Button size="lg" className="w-fit opacity-50 pointer-events-none">
              Coming Soon
            </Button>

            <Suspense fallback={null}>
              <OramaOneInline />
            </Suspense>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   9. ROADMAP — Milestone-based (no specific years)
   ═══════════════════════════════════════════ */
function Roadmap() {
  const [activeIndex, setActiveIndex] = useState(0);

  const milestones = [
    {
      status: "live" as const,
      phase: "Testnet",
      title: "Network Launch",
      items: [
        "Network live — no staking required to run a node",
        "Node runners begin earning $ORAMA block rewards",
        "Full infrastructure stack (SQL, Cache, Storage, PubSub, DNS)",
        "PLONK trusted setup ceremony",
        "Bug bounty program",
      ],
    },
    {
      status: "building" as const,
      phase: "Testnet Expansion",
      title: "AI & Commerce",
      items: [
        "AI Marketplace beta — host and consume AI models",
        "Angels framework — autonomous AI agents on-chain",
        "Compute provider registration",
        "Orama One pre-orders",
      ],
    },
    {
      status: "planned" as const,
      phase: "Testnet Maturity",
      title: "300-Node Threshold",
      items: [
        "Target 300 verified independent nodes for genesis",
        "DeBros NFT migration preparation (Solana snapshot)",
        "Bonding curve live on testnet",
        "Native order book testing ($ORAMA/BTC)",
      ],
    },
    {
      status: "planned" as const,
      phase: "Mainnet",
      title: "Full Production Launch",
      items: [
        "BTC bridge live — deposit and withdraw BTC",
        "Native DEX live — protocol order book for $ORAMA/BTC",
        "Staking activated (1,000 $ORAMA minimum)",
        "DeBros NFT bridge revenue begins",
        "On-chain governance live",
      ],
    },
    {
      status: "planned" as const,
      phase: "Post-Launch",
      title: "Long-Term Evolution",
      items: [
        "L2 rollup support (optimistic and zk)",
        "AI Marketplace expansion",
        "Post-quantum signature upgrade",
        "Orama One general availability",
        "Bonding curve sunset when organic liquidity is sufficient",
      ],
    },
  ];

  const statusConfig = {
    live: { dot: "active" as const, label: "LIVE" },
    building: { dot: "warning" as const, label: "IN PROGRESS" },
    planned: { dot: "neutral" as const, label: "PLANNED" },
  };

  const active = milestones[activeIndex];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Roadmap"
          subtitle="Milestone-based — mainnet launches when 300 nodes are verified."
        />
      </AnimateIn>

      <div className="mt-8">
        <AnimateIn>
          <div className="flex gap-1 mb-6 overflow-x-auto pb-2">
            {milestones.map((m, i) => (
              <button
                key={m.phase}
                type="button"
                onClick={() => setActiveIndex(i)}
                className={`flex items-center gap-2 px-4 py-2.5 text-xs font-mono tracking-wider uppercase rounded-full transition-all duration-200 cursor-pointer whitespace-nowrap ${
                  i === activeIndex
                    ? "bg-accent/10 border border-accent/30 text-accent"
                    : "border border-border text-muted hover:text-fg hover:border-border/80"
                }`}
              >
                <StatusDot status={statusConfig[m.status].dot} />
                {m.phase}
              </button>
            ))}
          </div>
        </AnimateIn>

        <AnimateIn>
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col gap-4 p-2">
              <div className="flex items-center gap-3">
                <StatusDot status={statusConfig[active.status].dot} />
                <span className="text-xs font-mono text-muted tracking-wider uppercase">
                  {statusConfig[active.status].label}
                </span>
              </div>
              <h3 className="font-display font-bold text-2xl text-fg">{active.title}</h3>
              <ul className="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-2">
                {active.items.map((item) => (
                  <li key={item} className="flex items-start gap-2">
                    <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                    <span className="text-sm text-muted">{item}</span>
                  </li>
                ))}
              </ul>
            </div>
          </DashedPanel>
        </AnimateIn>

        <div className="flex gap-1 mt-4">
          {milestones.map((m, i) => (
            <div
              key={m.phase}
              className={`h-1 flex-1 rounded-full transition-all duration-500 ${
                i <= activeIndex
                  ? m.status === "live" ? "bg-accent-2" : "bg-accent/50"
                  : "bg-border"
              }`}
            />
          ))}
        </div>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   9. PARTNERS — DeBros + Orama Proxy
   ═══════════════════════════════════════════ */
function Partners() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Partners"
          subtitle="The teams building and powering the Orama Network."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-8">
        {/* DeBros */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-5 p-4">
              <div className="flex items-center gap-4">
                <div className="w-16 h-16 shrink-0 flex items-center justify-center bg-white/[0.03] border border-border rounded-lg p-2">
                  <img src={debrosIcon} alt="DeBros" className="w-full h-full object-contain" />
                </div>
                <div>
                  <h3 className="font-display font-bold text-xl text-fg">DeBros</h3>
                  <span className="text-xs font-mono text-accent tracking-wider uppercase">
                    Core Team
                  </span>
                </div>
              </div>

              <p className="text-muted leading-relaxed">
                The engineering team behind Orama Network. Every line of code,
                every node, every protocol decision — built in-house. DeBros is
                focused on one thing: replacing centralized cloud infrastructure
                with something better.
              </p>

              <div className="flex flex-wrap gap-2">
                <span className="px-3 py-1 border border-border text-xs font-mono text-muted rounded-full">Open Source</span>
                <span className="px-3 py-1 border border-border text-xs font-mono text-muted rounded-full">Go + TypeScript</span>
                <span className="px-3 py-1 border border-border text-xs font-mono text-muted rounded-full">Full Stack</span>
              </div>

              <div className="flex gap-2">
                <Button asChild variant="ghost" size="sm">
                  <a href="https://debros.io" target="_blank" rel="noopener noreferrer">
                    debros.io <ExternalLink className="w-3 h-3 ml-1" />
                  </a>
                </Button>
                <Button asChild variant="ghost" size="sm">
                  <a href="https://github.com/DeBrosDAO" target="_blank" rel="noopener noreferrer">
                    GitHub <ExternalLink className="w-3 h-3 ml-1" />
                  </a>
                </Button>
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* ICXCNIKA */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-5 p-4">
              <div className="flex items-center gap-4">
                <div className="w-16 h-16 shrink-0 flex items-center justify-center bg-white/[0.03] border border-border rounded-lg p-2">
                  <img src="/images/icxcnika.webp" alt="ICXCNIKA" className="w-full h-full object-contain" />
                </div>
                <div>
                  <h3 className="font-display font-bold text-xl text-fg">ICXCNIKA</h3>
                  <span className="text-xs font-mono text-accent tracking-wider uppercase">
                    Partner
                  </span>
                </div>
              </div>

              <p className="text-muted leading-relaxed">
                ICXCNIKA is an early partner and supporter of the Orama Network,
                helping drive adoption and growth of decentralized infrastructure
                across the ecosystem.
              </p>

              <Button asChild variant="ghost" size="sm" className="w-fit">
                <a href="https://icxcnika.io/" target="_blank" rel="noopener noreferrer">
                  icxcnika.io <ExternalLink className="w-3 h-3 ml-1" />
                </a>
              </Button>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   9b. SPONSORS
   ═══════════════════════════════════════════ */

/* ═══════════════════════════════════════════
   10. FAQ
   ═══════════════════════════════════════════ */
function FAQ() {
  const [openIndex, setOpenIndex] = useState<number | null>(null);

  const faqs = [
    {
      question: "Is Orama Network live or still in development?",
      answer: "The testnet is live with nodes running across multiple environments. AnChat, a fully decentralized messenger, is the first production app running on it. The developer platform, playground, and infrastructure stack (databases, caching, storage, DNS, serverless) are all operational. Mainnet launches when 300 independent nodes are verified — no shortcuts, no exceptions.",
    },
    {
      question: "How is Orama different from Akash, Filecoin, or other DePIN projects?",
      answer: "Most decentralized infrastructure projects solve one piece — Filecoin does storage, Akash does compute. Orama is a full cloud platform: databases, caching, DNS, pub/sub, serverless, file storage, and compute — all in one network. Plus, it has its own L1 blockchain with pure WASM smart contracts, a native BTC bridge, per-transaction zk-SNARK privacy, and a built-in AI Marketplace. Developers deploy real apps, not just smart contracts.",
    },
    {
      question: "What is the $ORAMA token used for?",
      answer: "$ORAMA is the native token with a 210M hard cap and zero pre-mine — 100% mined by running nodes. Operators stake it (1,000 minimum at mainnet) to validate blocks. Developers pay for compute, storage, and bandwidth in $ORAMA. Base fees are burned, making the token increasingly deflationary as usage grows. The only way to acquire $ORAMA is to mine it by running a node or buy it with BTC on the native order book or bonding curve.",
    },
    {
      question: "Can I run a node today?",
      answer: "During testnet, no staking is required to run a node. Any operator can participate and earn $ORAMA block rewards with zero stake. Testnet tokens are real — they carry over to mainnet with no reset. Join the waitlist via our Telegram (t.me/debrosportal) to get notified when public onboarding opens.",
    },
    {
      question: "What programming languages can I use for smart contracts?",
      answer: "Orama uses a pure WASM virtual machine — no EVM, no Solidity. You can write smart contracts in any language that compiles to WebAssembly: Rust, Go, TypeScript, C++, and more. This gives developers full freedom to use the languages and tools they already know.",
    },
    {
      question: "What is the BTC-only economy?",
      answer: "Orama has exactly two assets: BTC and $ORAMA. No stablecoins, no wrapped altcoins, no fiat pegs. To acquire $ORAMA, you bridge BTC onto Orama and trade on the native order book or bonding curve. This creates zero counterparty risk beyond Bitcoin itself and makes Orama's economy entirely self-contained.",
    },
    {
      question: "Who is behind Orama Network?",
      answer: "Orama Network is built by DeBros — a focused engineering team building decentralized infrastructure from the ground up. Every line of code, every node, every protocol decision is built in-house. The project is open source and the team is active on GitHub, Telegram, and X.",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="FAQ"
          subtitle="Common questions from developers and investors."
        />
      </AnimateIn>

      <div className="flex flex-col gap-2 mt-8">
        {faqs.map((faq, i) => (
          <AnimateIn key={i}>
            <button
              type="button"
              onClick={() => setOpenIndex(openIndex === i ? null : i)}
              className="w-full text-left"
            >
              <DashedPanel withBackground>
                <div className="flex flex-col gap-0 p-2">
                  <div className="flex items-center justify-between gap-4">
                    <h3 className="font-display font-bold text-fg text-sm sm:text-base">
                      {faq.question}
                    </h3>
                    <ChevronRight
                      className={`w-4 h-4 shrink-0 text-muted transition-transform duration-200 ${
                        openIndex === i ? "rotate-90" : ""
                      }`}
                    />
                  </div>
                  <div
                    className={`overflow-hidden transition-all duration-300 ${
                      openIndex === i ? "max-h-60 mt-3 opacity-100" : "max-h-0 opacity-0"
                    }`}
                  >
                    <p className="text-muted text-sm leading-relaxed">{faq.answer}</p>
                  </div>
                </div>
              </DashedPanel>
            </button>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   11. FINAL CTA
   ═══════════════════════════════════════════ */
function FinalCTA() {
  return (
    <Section padding="wide">
      <AnimateIn>
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col items-center text-center gap-6 py-8">
            <Badge variant="outline" className="w-fit">
              TESTNET LIVE
            </Badge>

            <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
              The decentralized cloud is being built.
              <br />
              <span className="text-accent">Be part of it.</span>
            </h2>

            <p className="text-muted max-w-lg leading-relaxed">
              No tokens to buy beforehand. No presale to miss. Run a node, earn $ORAMA,
              and be part of the only blockchain where everyone starts equal.
            </p>

            <div className="flex flex-wrap items-center gap-3 justify-center">
              <Button asChild size="lg">
                <Link to="/investors">
                  Become an Investor
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </Link>
              </Button>
              <Button asChild variant="dashed" size="lg">
                <a href="https://t.me/debrosportal" target="_blank" rel="noopener noreferrer">
                  Join the Waitlist
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </a>
              </Button>
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
export default function Home() {
  return (
    <Page title="Orama Network — The Decentralized Cloud">
      <HomeHero />
      <Section padding="none"><CrosshairDivider /></Section>
      <InvestorSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <WhitelistSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <HowItWorks />
      <Section padding="none"><CrosshairDivider /></Section>
      <AnChatShowcase />
      <Section padding="none"><CrosshairDivider /></Section>
      <BlockchainSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <ComputeSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <OramaOneSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <Roadmap />
      <Section padding="none"><CrosshairDivider /></Section>
      <Partners />
      <Section padding="none"><CrosshairDivider /></Section>
      <SponsorsShowcase />
      <Section padding="none"><CrosshairDivider /></Section>
      <FAQ />
      <Section padding="none"><CrosshairDivider /></Section>
      <FinalCTA />
    </Page>
  );
}
