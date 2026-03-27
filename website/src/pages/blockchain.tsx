import { useState, lazy, Suspense } from "react";
import { Link } from "react-router";
import { Page } from "../components/layout/page";
import { SplitText } from "../components/ui/split-text";

const ConsensusScene = lazy(() =>
  import("../components/landing/consensus-scene").then((m) => ({
    default: m.ConsensusScene,
  })),
);

const OramaOneInline = lazy(() =>
  import("../components/landing/orama-one-scene").then((m) => ({
    default: m.OramaOneInline,
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
  Coins,
  Vote,
  Cpu,
  Server,
  Wallet,
  Repeat,
  ChevronRight,
  Zap,
  Shield,
  Lock,
  Eye,
  EyeOff,
} from "lucide-react";

/* ═══════════════════════════════════════════
   1. HERO
   ═══════════════════════════════════════════ */
function BlockchainHero() {
  return (
    <Section padding="wide">
      <div className="flex flex-col items-center text-center min-h-[70vh] justify-center gap-6 max-w-3xl mx-auto">
        <span
          className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full"
          style={{ border: `1px dashed ${SILVER.mid}`, color: SILVER.light }}
        >
          STANDALONE L1 BLOCKCHAIN
        </span>

        <h1 className="font-display font-bold text-4xl lg:text-6xl leading-tight">
          <SplitText
            text="The eternal decentralized"
            className="text-fg"
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
          />
          <br />
          <SplitText
            text="computer & financial system."
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

        <p className="text-muted text-sm leading-relaxed max-w-lg">
          Orama combines Bitcoin-grade scarcity with a global decentralized cloud.
          210,000,000 $ORAMA hard cap. Zero pre-mine. 100% mined. BTC-only economy.
          Pure WASM smart contracts. Built for a 1,000-year horizon.
        </p>

        <div className="flex flex-wrap items-center gap-3 justify-center pt-4">
          <Button asChild variant="ghost" size="lg">
            <a href="/orama-whitepaper-v3.pdf" target="_blank" rel="noopener noreferrer">
              Whitepaper
              <ExternalLink className="w-3.5 h-3.5 ml-2" />
            </a>
          </Button>
          <Button asChild variant="ghost" size="lg">
            <Link to="/node">
              Run a Node
              <ArrowRight className="w-3.5 h-3.5 ml-2" />
            </Link>
          </Button>
        </div>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   2. TWO LAYERS
   ═══════════════════════════════════════════ */
function TwoLayers() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Two Layers, One Chain"
          subtitle="An immutable financial core and a modular compute layer — tightly integrated, never separable."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-8">
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full border-accent/30">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-2">
                <Lock className="w-6 h-6 text-accent" />
                <span className="text-xs font-mono text-accent tracking-wider uppercase">Immutable</span>
              </div>
              <h3 className="font-display font-bold text-fg text-lg">Financial Core</h3>
              <p className="text-muted text-sm leading-relaxed">
                BTC + $ORAMA economics. 210M hard cap, emission schedule, halving, BTC-only economy,
                100% mining distribution, and BTC bridge security. Designed to be unchangeable for 1,000 years.
                No governance vote can ever modify these rules.
              </p>
            </div>
          </DashedPanel>
        </AnimateIn>
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-2">
                <Cpu className="w-6 h-6 text-accent" />
                <span className="text-xs font-mono text-muted tracking-wider uppercase">Upgradable</span>
              </div>
              <h3 className="font-display font-bold text-fg text-lg">Compute Layer</h3>
              <p className="text-muted text-sm leading-relaxed">
                Pure WASM execution, distributed SQL, KV store, IPFS, serverless functions,
                AI Marketplace, and native BTC bridge. Upgradable via governance but can never
                break the money layer. Write contracts in Rust, Go, TypeScript, or C++.
              </p>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   3. CONSENSUS — THREE PILLARS
   ═══════════════════════════════════════════ */
function ThreePillars() {
  const pillars = [
    {
      icon: Coins,
      title: "Proof of Stake",
      description: "Classic staking for economic security. Validators stake $ORAMA as collateral. Minimum 1,000 $ORAMA at mainnet. Slashing for double-signing (100%), downtime (5-30%), and false attestation (50%).",
      primary: false,
    },
    {
      icon: Zap,
      title: "Proof of Contribution",
      description: "Real work performed, measured on-chain every epoch (1 hour). Uptime 40%, bandwidth 30%, compute/storage 20%, reliability 10%. Contribution score directly multiplies your earning power.",
      primary: true,
    },
    {
      icon: Server,
      title: "Proof of Infrastructure",
      description: "Nodes running OramaOS receive a 1.5x multiplier via TPM-based remote attestation. A node runner with modest stake but perfect uptime and OramaOS earns more than a whale who only stakes.",
      primary: false,
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Hybrid Consensus"
          subtitle="Three mechanisms combined — stake, contribution, and infrastructure."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-8">
        {pillars.map((p) => (
          <AnimateIn key={p.title}>
            <DashedPanel withCorners={p.primary} withBackground className={`h-full ${p.primary ? "border-accent/30" : ""}`}>
              <div className="flex flex-col gap-4 p-3">
                <div className="flex items-center gap-2">
                  <p.icon className="w-6 h-6 text-accent" />
                  {p.primary && (
                    <span className="text-xs font-mono text-accent tracking-wider uppercase">Core</span>
                  )}
                </div>
                <h3 className="font-display font-bold text-fg">{p.title}</h3>
                <p className="text-muted text-sm leading-relaxed">{p.description}</p>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>

      {/* Effective Power Formula */}
      <AnimateIn>
        <div className="mt-6">
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col gap-4 p-4">
              <h3 className="font-display font-bold text-fg text-center">Effective Power Formula</h3>
              <div className="text-center font-mono text-sm sm:text-base text-fg py-3 px-4 rounded-lg" style={{ background: "rgba(255,255,255,0.03)" }}>
                Effective Power = Staked $ORAMA × (1 + Contribution Score) × Infrastructure Multiplier
              </div>
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                <div className="flex flex-col gap-1 items-center text-center">
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Block Time</span>
                  <span className="font-display font-bold text-fg">6 seconds</span>
                </div>
                <div className="flex flex-col gap-1 items-center text-center">
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Epoch</span>
                  <span className="font-display font-bold text-fg">1 hour</span>
                </div>
                <div className="flex flex-col gap-1 items-center text-center">
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Blocks/Day</span>
                  <span className="font-display font-bold text-fg">14,400</span>
                </div>
                <div className="flex flex-col gap-1 items-center text-center">
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Finality</span>
                  <span className="font-display font-bold text-fg">BFT / 1 hr</span>
                </div>
              </div>
            </div>
          </DashedPanel>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   4. TOKENOMICS — Emission Schedule
   ═══════════════════════════════════════════ */
function TokenomicsSection() {
  const utilities = [
    { icon: Zap, title: "Gas Currency", desc: "All transactions require $ORAMA. Base fee burned. EIP-1559 congestion multiplier." },
    { icon: Coins, title: "Staking", desc: "Validators stake $ORAMA. Minimum 1,000 at mainnet. Slashing for malicious behavior." },
    { icon: Vote, title: "Governance", desc: "25% of total voting power (quadratic). NFT holders control the other 75%." },
    { icon: Wallet, title: "Service Payments", desc: "SQL, KV, IPFS, compute, AI inference, DEX trades — all paid in $ORAMA." },
  ];

  const emissionSchedule = [
    { era: "1", years: "1-2", reward: "100 $ORAMA", annual: "~52.5M", cumulative: "~105M" },
    { era: "2", years: "3-4", reward: "50 $ORAMA", annual: "~26.25M", cumulative: "~157.5M" },
    { era: "3", years: "5-6", reward: "25 $ORAMA", annual: "~13.1M", cumulative: "~183.7M" },
    { era: "4", years: "7-8", reward: "12.5 $ORAMA", annual: "~6.6M", cumulative: "~196.9M" },
    { era: "5", years: "9-10", reward: "6.25 $ORAMA", annual: "~3.3M", cumulative: "~203.5M" },
    { era: "6+", years: "11+", reward: "Halving", annual: "Decreasing", cumulative: "210M cap" },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Tokenomics"
          subtitle="$ORAMA — 210,000,000 hard cap. Zero pre-mine. 100% mined."
        />
      </AnimateIn>

      {/* Token overview + utility */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8 items-center">
        <AnimateIn>
          <div className="flex flex-col gap-5">
            <span
              className="font-display font-bold text-4xl"
              style={{ background: SILVER.gradient, WebkitBackgroundClip: "text", WebkitTextFillColor: "transparent" }}
            >
              $ORAMA
            </span>
            <div className="grid grid-cols-3 gap-3">
              <DashedPanel withBackground>
                <div className="flex flex-col gap-1 p-2">
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Hard Cap</span>
                  <span className="font-display font-bold text-fg">210,000,000</span>
                </div>
              </DashedPanel>
              <DashedPanel withBackground>
                <div className="flex flex-col gap-1 p-2">
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Pre-Mine</span>
                  <span className="font-display font-bold text-fg">0%</span>
                </div>
              </DashedPanel>
              <DashedPanel withCorners withBackground className="border-accent/30">
                <div className="flex flex-col gap-1 p-2">
                  <span className="text-xs font-mono text-accent tracking-wider uppercase">Smallest Unit</span>
                  <span className="font-display font-bold text-fg">1M rays</span>
                </div>
              </DashedPanel>
            </div>

            {/* Block reward split */}
            <DashedPanel withBackground>
              <div className="flex flex-col gap-3 p-2">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">Block Reward Distribution</span>
                <div className="flex h-3 rounded-full overflow-hidden">
                  <div className="h-full" style={{ width: "80%", background: SILVER.light }} title="Block Proposer" />
                  <div className="h-full" style={{ width: "20%", background: SILVER.dark }} title="Bonding Curve" />
                </div>
                <div className="flex items-center justify-between text-xs font-mono text-muted">
                  <div className="flex items-center gap-1.5">
                    <span className="w-2 h-2 rounded-sm" style={{ background: SILVER.light }} />
                    <span>80% Block Proposer</span>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <span className="w-2 h-2 rounded-sm" style={{ background: SILVER.dark }} />
                    <span>20% Bonding Curve (cap 21M)</span>
                  </div>
                </div>
              </div>
            </DashedPanel>

            <p className="text-muted text-sm leading-relaxed">
              Every single $ORAMA is earned by running a node. No team allocation, no investor round,
              no foundation reserve, no airdrop. The creators earn tokens the same way as everyone else.
              50% of total supply emitted in the first 2 years. Bitcoin-style halving every 2 years.
            </p>
          </div>
        </AnimateIn>
        <AnimateIn>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            {utilities.map((u) => (
              <DashedPanel key={u.title} withBackground className="h-full">
                <div className="flex flex-col gap-2 p-2">
                  <u.icon className="w-4 h-4 text-accent" />
                  <h4 className="font-display font-bold text-sm text-fg">{u.title}</h4>
                  <p className="text-muted text-xs leading-relaxed">{u.desc}</p>
                </div>
              </DashedPanel>
            ))}
          </div>
        </AnimateIn>
      </div>

      {/* Emission schedule table + fee schedule */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8">
        {/* Emission schedule */}
        <AnimateIn>
          <DashedPanel withCorners withBackground>
            <div className="overflow-x-auto p-2">
              <span className="text-xs font-mono text-muted tracking-wider uppercase block mb-3 px-1">Emission Schedule</span>
              <table className="w-full text-left text-xs sm:text-sm">
                <thead>
                  <tr className="border-b border-dashed border-border">
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-2 pr-3">Era</th>
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-2 pr-3">Years</th>
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-2 pr-3">Block Reward</th>
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-2">Cumulative</th>
                  </tr>
                </thead>
                <tbody>
                  {emissionSchedule.map((row) => (
                    <tr key={row.era} className="border-b border-border/50">
                      <td className="text-sm text-fg py-2 pr-3 font-mono">{row.era}</td>
                      <td className="text-sm text-fg py-2 pr-3 font-mono">{row.years}</td>
                      <td className="text-sm text-fg py-2 pr-3 font-mono">{row.reward}</td>
                      <td className="text-sm text-fg py-2 font-mono">{row.cumulative}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Fee schedule */}
        <AnimateIn>
          <DashedPanel withBackground>
            <div className="overflow-x-auto p-2">
              <span className="text-xs font-mono text-muted tracking-wider uppercase block mb-3 px-1">Genesis Fee Schedule</span>
              <table className="w-full text-left text-xs sm:text-sm">
                <thead>
                  <tr className="border-b border-dashed border-border">
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-2 pr-3">Operation</th>
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-2">Cost</th>
                  </tr>
                </thead>
                <tbody>
                  {[
                    { op: "Transfer", cost: "1,000 rays" },
                    { op: "WASM Execution", cost: "1,000 rays / 1M instr." },
                    { op: "SQL Query", cost: "500 rays" },
                    { op: "IPFS Storage", cost: "10,000 rays / MB" },
                    { op: "KV Read/Write", cost: "200 rays" },
                    { op: "DEX Trade", cost: "1,000 rays" },
                    { op: "Private (zk-SNARK)", cost: "4x public" },
                  ].map((row) => (
                    <tr key={row.op} className="border-b border-border/50">
                      <td className="text-sm text-fg py-2 pr-3">{row.op}</td>
                      <td className="text-sm text-fg py-2 font-mono">{row.cost}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>

      <AnimateIn>
        <div className="flex flex-wrap items-center justify-center gap-3 mt-8">
          <Button asChild variant="ghost" size="lg">
            <a href="/orama-whitepaper-v3.pdf" target="_blank" rel="noopener noreferrer">
              Full Tokenomics in Whitepaper
              <ExternalLink className="w-3.5 h-3.5 ml-2" />
            </a>
          </Button>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   5. HOW TO ACQUIRE $ORAMA
   ═══════════════════════════════════════════ */
function AcquireSection() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="How to Acquire $ORAMA"
          subtitle="No pre-sale. No airdrop. Two ways to get $ORAMA."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-8">
        {/* Mine it */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full border-accent/30">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-2">
                <Server className="w-6 h-6 text-accent" />
                <span className="text-xs font-mono text-accent tracking-wider uppercase">Primary</span>
              </div>
              <h3 className="font-display font-bold text-xl text-fg">Run a Node</h3>
              <p className="text-sm text-muted leading-relaxed">
                Run a node and mine $ORAMA. During testnet, no staking required — any node operator
                can participate and earn. Testnet tokens carry over to mainnet. At mainnet,
                1,000 $ORAMA minimum stake activates, but testnet runners will already have earned more than enough.
              </p>
              <ul className="flex flex-col gap-2">
                {[
                  "Era 1: 100 $ORAMA per block (~52.5M/year)",
                  "80% goes directly to the block proposer",
                  "OramaOS = 1.5x infrastructure multiplier",
                  "No staking needed during testnet",
                  "Testnet tokens are real — they carry over",
                ].map((item) => (
                  <li key={item} className="flex items-start gap-2">
                    <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                    <span className="text-xs text-muted">{item}</span>
                  </li>
                ))}
              </ul>
              <Button asChild variant="ghost" size="sm" className="w-fit">
                <Link to="/node">
                  Learn About Running a Node
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </Link>
              </Button>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Buy from bonding curve */}
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-2">
                <Coins className="w-6 h-6 text-accent" />
                <span className="text-xs font-mono text-muted tracking-wider uppercase">Secondary</span>
              </div>
              <h3 className="font-display font-bold text-xl text-fg">Buy with BTC</h3>
              <p className="text-sm text-muted leading-relaxed">
                Bridge BTC onto Orama and purchase $ORAMA from the protocol bonding curve or the
                native order book. The bonding curve is always available as a guaranteed liquidity backstop.
                Price follows a square root function — cheap early, expensive later.
              </p>
              <ul className="flex flex-col gap-2">
                {[
                  "Bonding curve: Price = k x sqrt(total_sold)",
                  "k = 0.0000000006 BTC at genesis",
                  "Max 21M tokens via bonding curve",
                  "~38.5 BTC fills the entire curve",
                  "BTC reserve directly backs the bridge",
                ].map((item) => (
                  <li key={item} className="flex items-start gap-2">
                    <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                    <span className="text-xs text-muted">{item}</span>
                  </li>
                ))}
              </ul>
              <p className="text-xs text-muted/60 font-mono">
                BTC only. No ETH, SOL, or stablecoins accepted.
              </p>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   6. ORAMA ONE — Hardware Node
   ═══════════════════════════════════════════ */
function OramaOneBlockchain() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Orama One"
          subtitle="The people's hardware node. Plug in, connect, earn."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8 items-center">
        <AnimateIn>
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col items-center text-center gap-6 py-8 px-4">
              <span
                className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full"
                style={{ border: `1px dashed ${SILVER.mid}`, color: SILVER.light }}
              >
                HARDWARE NODE
              </span>

              <h3
                className="font-display font-bold text-3xl"
                style={{
                  background: SILVER.gradient,
                  WebkitBackgroundClip: "text",
                  WebkitTextFillColor: "transparent",
                }}
              >
                Orama One
              </h3>

              <p className="text-muted text-sm leading-relaxed max-w-sm">
                A purpose-built, 3D-printed hardware node. Pre-loaded with OramaOS.
                Plug in power, connect to internet, it joins the network automatically.
                Silent, low-power, designed for your desk.
              </p>

              <p className="text-xs font-mono text-muted/50 tracking-wider uppercase">
                Coming soon — details to be announced
              </p>

              <Suspense fallback={null}>
                <OramaOneInline />
              </Suspense>
            </div>
          </DashedPanel>
        </AnimateIn>

        <AnimateIn>
          <div className="flex flex-col gap-5">
            <h3 className="font-display font-bold text-xl text-fg">
              OramaOS — hardened like a hardware wallet
            </h3>

            <p className="text-muted text-sm leading-relaxed">
              OramaOS is a custom minimal operating system purpose-built for Orama nodes.
              Running OramaOS provides the{" "}
              <span className="text-fg font-semibold">1.5x Infrastructure Multiplier</span>,
              verified via TPM-based remote attestation every epoch.
            </p>

            <ul className="flex flex-col gap-2">
              {[
                "No SSH — zero remote attack surface",
                "Read-only root filesystem — tamper-proof",
                "Full-disk encryption with Shamir secret sharing",
                "Atomic updates with automatic rollback",
                "Single root process (orama-agent) — minimal OS",
                "TPM attestation — cryptographic proof, not trust",
                "4+ cores, 8GB RAM, 256GB NVMe, TPM 2.0",
              ].map((item) => (
                <li key={item} className="flex items-start gap-2">
                  <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                  <span className="text-xs text-muted">{item}</span>
                </li>
              ))}
            </ul>
          </div>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   7. BTC BRIDGE & NATIVE DEX
   ═══════════════════════════════════════════ */
function BtcBridgeAndDex() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="BTC-Only Economy"
          subtitle="Two assets exist on Orama: BTC and $ORAMA. Nothing else."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-8">
        {/* BTC Bridge */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-2">
                <Shield className="w-5 h-5 text-accent" />
                <h3 className="font-display font-bold text-fg">Trust-Minimized BTC Bridge</h3>
              </div>
              <p className="text-sm text-muted leading-relaxed">
                Built into the protocol from genesis. Deposit BTC, receive native BTC on Orama 1:1.
                Withdraw back to Bitcoin mainnet with Bitcoin-level security.
              </p>
              <ul className="flex flex-col gap-2">
                {[
                  "Bitcoin light-client + zk-proofs + BitVM fraud proofs",
                  "1-of-N honest assumption — one honest validator is enough",
                  "0.25% bridge fee (50% validators, 50% auto-swap for Team NFTs)",
                  "Protocol reserve from bonding curve backs the bridge",
                  "Auto-halt on anomalous withdrawals (>10% in one epoch)",
                  "Min 0.001 BTC per bridge transaction",
                ].map((item) => (
                  <li key={item} className="flex items-start gap-2">
                    <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                    <span className="text-xs text-muted">{item}</span>
                  </li>
                ))}
              </ul>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Native Order Book */}
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-2">
                <Repeat className="w-5 h-5 text-accent" />
                <h3 className="font-display font-bold text-fg">Native Order Book</h3>
              </div>
              <p className="text-sm text-muted leading-relaxed">
                A protocol-native order book — a chain primitive, not a smart contract.
                One pair: $ORAMA/BTC. Pure price discovery.
                No intermediary. No privileged LP class.
              </p>
              <ul className="flex flex-col gap-2">
                {[
                  "Order book, NOT an AMM — no liquidity pools",
                  "Limit orders, market orders, cancellation",
                  "Randomized in-block ordering — no MEV/front-running",
                  "Callable from WASM contracts and external RPC",
                  "No listing fees, no gatekeepers, permissionless",
                  "Custom WASM tokens trade against $ORAMA",
                ].map((item) => (
                  <li key={item} className="flex items-start gap-2">
                    <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                    <span className="text-xs text-muted">{item}</span>
                  </li>
                ))}
              </ul>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>

      {/* Asset hierarchy */}
      <AnimateIn>
        <div className="mt-6">
          <DashedPanel withBackground>
            <div className="flex flex-col sm:flex-row items-center justify-center gap-3 p-4 font-mono text-sm">
              <span className="font-bold" style={{ color: "#F7931A" }}>BTC</span>
              <span className="text-muted">{"<->"}</span>
              <span className="text-xs text-muted">protocol order book</span>
              <span className="text-muted">{"<->"}</span>
              <span className="font-bold" style={{ background: SILVER.gradient, WebkitBackgroundClip: "text", WebkitTextFillColor: "transparent" }}>$ORAMA</span>
              <span className="text-muted">{"<->"}</span>
              <span className="text-xs text-muted">WASM DEX contracts</span>
              <span className="text-muted">{"<->"}</span>
              <span className="font-bold text-fg">Custom Tokens</span>
            </div>
          </DashedPanel>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   8. PRIVACY
   ═══════════════════════════════════════════ */
function PrivacySection() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Per-Transaction Privacy"
          subtitle="PLONK zk-SNARKs with a universal trusted setup — upgrade privacy without new ceremonies."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-8">
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col items-center text-center gap-3 p-5">
              <Eye className="w-8 h-8 text-accent" />
              <h3 className="font-display font-bold text-fg text-lg">Public (Default)</h3>
              <p className="text-xs text-muted max-w-xs">
                Fully transparent, lowest cost. Standard gas fees apply.
              </p>
            </div>
          </DashedPanel>
        </AnimateIn>
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full border-accent/30">
            <div className="flex flex-col items-center text-center gap-3 p-5">
              <EyeOff className="w-8 h-8 text-accent" />
              <h3 className="font-display font-bold text-fg text-lg">Private (4x Gas)</h3>
              <p className="text-xs text-muted max-w-xs">
                PLONK zk-SNARKs shield sender, receiver, and amount.
                Only participants know the details. Transaction existence is still visible on-chain.
              </p>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   9. ANGELS & AI MARKETPLACE
   ═══════════════════════════════════════════ */
function AngelsSection() {
  const steps = [
    { number: "01", title: "Build", desc: "Create an AI agent or model in any WASM-compatible language" },
    { number: "02", title: "Deploy", desc: "Register on the AI Marketplace — pricing set by the provider" },
    { number: "03", title: "Earn", desc: "Get paid in $ORAMA per API call. Transparent, competitive marketplace" },
  ];

  const useCases = [
    { icon: Cpu, title: "Model Hosting", desc: "Host AI models, get paid per request" },
    { icon: Zap, title: "Angels", desc: "Autonomous agents with on-chain access" },
    { icon: Wallet, title: "On-Chain AI", desc: "Angels hold $ORAMA, execute transactions" },
    { icon: Server, title: "Compute", desc: "Capped at 10% of total network nodes" },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="AI Marketplace & Angels"
          subtitle="A protocol-level primitive for hosting and consuming AI models and autonomous agents."
        />
      </AnimateIn>

      {/* How it works — 3 steps */}
      <div className="grid grid-cols-3 gap-3 mt-8">
        {steps.map((step) => (
          <AnimateIn key={step.number}>
            <DashedPanel withBackground className="h-full">
              <div className="flex flex-col items-center text-center gap-2 p-4">
                <span className="text-xs font-mono text-accent tracking-wider">{step.number}</span>
                <h3 className="font-display font-bold text-fg">{step.title}</h3>
                <p className="text-muted text-xs">{step.desc}</p>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>

      {/* Angel Marketplace preview */}
      <AnimateIn>
        <div className="mt-6">
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col gap-5 p-4">
              <div className="flex items-center justify-between">
                <h3 className="font-display font-bold text-fg">What Angels Can Do</h3>
                <span className="text-xs font-mono text-muted/50 tracking-wider">COMING SOON</span>
              </div>

              <p className="text-sm text-muted leading-relaxed">
                Angels are autonomous AI agents that interact with Orama's on-chain primitives:
                SQL, KV store, IPFS, BTC bridge, and the native DEX. They hold $ORAMA,
                execute transactions, manage data, and interact with other Angels.
                Builders set per-request or subscription pricing in $ORAMA.
              </p>

              <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                {useCases.map((uc) => (
                  <div key={uc.title} className="flex flex-col items-center text-center gap-2 py-3">
                    <uc.icon className="w-5 h-5 text-accent" />
                    <span className="text-sm font-display font-bold text-fg">{uc.title}</span>
                    <span className="text-xs text-muted">{uc.desc}</span>
                  </div>
                ))}
              </div>

              <p className="text-xs text-muted text-center">
                Compute providers run standard nodes plus optional AI capacity. AI revenue is purely market-driven.
                The protocol caps compute-provider nodes at 10% of total network nodes.
              </p>
            </div>
          </DashedPanel>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   10. GOVERNANCE & NFTs
   ═══════════════════════════════════════════ */
function GovernanceSection() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="On-Chain Governance"
          subtitle="Fully on-chain. No off-chain snapshots. NFT holders control 75% of voting power."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-8">
        {[
          {
            title: "DeBros Team NFTs (100)",
            power: "40%",
            detail: "5 votes per NFT. 50% of bridge fees auto-swapped to $ORAMA. Emergency governance (24h). Migrated from Solana.",
            highlight: true,
          },
          {
            title: "DeBros NFTs (700)",
            power: "35%",
            detail: "1 vote per NFT. The community that built the network before the blockchain existed. Migrated from Solana.",
            highlight: false,
          },
          {
            title: "$ORAMA Holders",
            power: "25%",
            detail: "Quadratic voting: sqrt(tokens held). No whale can outweigh NFT holders. Open to all token holders.",
            highlight: false,
          },
        ].map((g) => (
          <AnimateIn key={g.title}>
            <DashedPanel withCorners={g.highlight} withBackground className={`h-full ${g.highlight ? "border-accent/30" : ""}`}>
              <div className="flex flex-col gap-3 p-3">
                <span className="font-display font-bold text-3xl text-accent">{g.power}</span>
                <h3 className="font-display font-bold text-fg">{g.title}</h3>
                <p className="text-muted text-sm leading-relaxed">{g.detail}</p>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>

      {/* Three tiers */}
      <AnimateIn>
        <div className="mt-6">
          <DashedPanel withBackground>
            <div className="overflow-x-auto p-2">
              <span className="text-xs font-mono text-muted tracking-wider uppercase block mb-3 px-1">Decision Tiers</span>
              <table className="w-full text-left text-xs sm:text-sm">
                <thead>
                  <tr className="border-b border-dashed border-border">
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-2 pr-3">Tier</th>
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-2 pr-3">Period</th>
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-2 pr-3">Threshold</th>
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-2">Scope</th>
                  </tr>
                </thead>
                <tbody>
                  {[
                    { tier: "Emergency", period: "24 hours", threshold: "60% Team NFTs", scope: "Security patches, exploits" },
                    { tier: "Protocol", period: "3 days", threshold: "66% all groups", scope: "Fee changes, new primitives, upgrades" },
                    { tier: "Constitutional", period: "14 days", threshold: "90% all groups", scope: "Governance structure, bridge fees" },
                  ].map((row) => (
                    <tr key={row.tier} className="border-b border-border/50">
                      <td className="text-sm text-fg py-2 pr-3 font-bold">{row.tier}</td>
                      <td className="text-sm text-fg py-2 pr-3 font-mono">{row.period}</td>
                      <td className="text-sm text-fg py-2 pr-3 font-mono">{row.threshold}</td>
                      <td className="text-sm text-muted py-2">{row.scope}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </DashedPanel>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   11. ROOTWALLET & OPEN SOURCE
   ═══════════════════════════════════════════ */
function RootWalletSection() {
  return (
    <Section>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <AnimateIn>
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col items-center text-center gap-3 p-5">
              <span
                className="font-display font-bold text-xl"
                style={{ background: SILVER.gradient, WebkitBackgroundClip: "text", WebkitTextFillColor: "transparent" }}
              >
                RootWallet
              </span>
              <p className="text-xs text-muted max-w-xs">
                The official wallet of the Orama Network. Manage $ORAMA, bridged BTC,
                governance votes, and Angel interactions — all in one place.
              </p>
              <Button asChild variant="ghost" size="sm">
                <a href="https://rootwallet.io" target="_blank" rel="noopener noreferrer">
                  rootwallet.io <ExternalLink className="w-3 h-3 ml-1" />
                </a>
              </Button>
            </div>
          </DashedPanel>
        </AnimateIn>

        <AnimateIn>
          <DashedPanel withBackground>
            <div className="flex flex-col items-center text-center gap-3 p-5">
              <span className="font-display font-bold text-xl text-fg">Open Source</span>
              <p className="text-xs text-muted max-w-xs">
                Orama Network is fully open source. All critical protocol components are verifiable.
                Bug bounty program from testnet launch.
              </p>
              <div className="flex flex-wrap gap-2 justify-center">
                <Button asChild variant="ghost" size="sm">
                  <a href="https://github.com/DeBrosDAO/orama" target="_blank" rel="noopener noreferrer">
                    Orama <ExternalLink className="w-3 h-3 ml-1" />
                  </a>
                </Button>
                <Button asChild variant="ghost" size="sm">
                  <a href="https://github.com/DeBrosDAO/network-ts-sdk" target="_blank" rel="noopener noreferrer">
                    SDK <ExternalLink className="w-3 h-3 ml-1" />
                  </a>
                </Button>
                <Button asChild variant="ghost" size="sm">
                  <a href="https://github.com/DeBrosDAO/orama-vault" target="_blank" rel="noopener noreferrer">
                    Vault <ExternalLink className="w-3 h-3 ml-1" />
                  </a>
                </Button>
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   12. ROADMAP
   ═══════════════════════════════════════════ */
function BlockchainRoadmap() {
  const phases = [
    {
      status: "live" as const,
      label: "NOW",
      title: "Infrastructure Live",
      items: [
        "50+ nodes running across devnet and testnet",
        "Core infrastructure operational (SQL, Cache, Storage, DNS)",
        "Orama Proxy integrated with Anyone Protocol",
        "AnChat Lite in closed beta",
      ],
    },
    {
      status: "building" as const,
      label: "TESTNET",
      title: "Blockchain Testnet",
      items: [
        "L1 blockchain testnet — nodes begin earning $ORAMA",
        "No staking required — all operators can participate",
        "Testnet tokens carry over to mainnet (no reset)",
        "PLONK trusted setup ceremony",
        "Bug bounty program launches",
        "AI Marketplace beta, Angels framework",
      ],
    },
    {
      status: "planned" as const,
      label: "TESTNET MATURITY",
      title: "300-Node Threshold",
      items: [
        "Reach 300 independent nodes — hard genesis requirement",
        "Bonding curve live on testnet",
        "Native order book testing",
        "DeBros NFT migration preparation",
        "Orama One pre-orders",
      ],
    },
    {
      status: "planned" as const,
      label: "MAINNET",
      title: "Full Launch",
      items: [
        "Full production launch — L1 blockchain live",
        "BTC bridge live with trust-minimized security",
        "Native DEX (order book) live — $ORAMA/BTC",
        "Staking activated (1,000 $ORAMA minimum)",
        "DeBros NFT bridge revenue begins",
        "On-chain governance live",
      ],
    },
  ];

  const statusConfig = {
    live: { dot: "active" as const, label: "LIVE" },
    building: { dot: "warning" as const, label: "IN PROGRESS" },
    planned: { dot: "neutral" as const, label: "PLANNED" },
  };

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Roadmap"
          subtitle="Milestone-based. No fixed dates. Ship when ready."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-8">
        {phases.map((phase) => (
          <AnimateIn key={phase.label}>
            <DashedPanel withCorners={phase.status === "live"} withBackground className={`h-full ${phase.status === "live" ? "border-accent/30" : ""}`}>
              <div className="flex flex-col gap-3 p-3">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <StatusDot status={statusConfig[phase.status].dot} />
                    <span className="text-xs font-mono text-muted tracking-wider uppercase">
                      {statusConfig[phase.status].label}
                    </span>
                  </div>
                  <span className="text-xs font-mono text-accent tracking-wider">{phase.label}</span>
                </div>
                <h3 className="font-display font-bold text-lg text-fg">{phase.title}</h3>
                <ul className="flex flex-col gap-2">
                  {phase.items.map((item) => (
                    <li key={item} className="flex items-start gap-2">
                      <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                      <span className="text-xs text-muted">{item}</span>
                    </li>
                  ))}
                </ul>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   13. FAQ
   ═══════════════════════════════════════════ */
function BlockchainFAQ() {
  const [openIndex, setOpenIndex] = useState<number | null>(null);

  const faqs = [
    {
      question: "Is this an EVM-compatible chain?",
      answer: "No. Orama is a pure WASM blockchain from genesis. Developers write smart contracts in Rust, Go, TypeScript, C++, or any language that compiles to WebAssembly. No EVM, no Solidity.",
    },
    {
      question: "Is this a fork of another chain?",
      answer: "No. Orama is built from first principles — a standalone L1 with its own consensus (hybrid PoS + Proof of Contribution + Proof of Infrastructure), its own VM (WASM), and its own economic model (BTC-only, 100% mined).",
    },
    {
      question: "How do I get $ORAMA?",
      answer: "Run a node and mine it. During testnet, no staking is required — anyone can participate. Alternatively, bridge BTC onto Orama and buy $ORAMA from the bonding curve or the native order book. There is no pre-sale, airdrop, or investor allocation.",
    },
    {
      question: "Can I pay with ETH or SOL?",
      answer: "No. Orama is a BTC-only economy. The only two assets on Orama are BTC (bridged from Bitcoin mainnet) and $ORAMA. No ETH, SOL, stablecoins, or other altcoins exist on the chain.",
    },
    {
      question: "When is mainnet?",
      answer: "Mainnet launches when the 300-node genesis requirement is met and the testnet has proven stability. The roadmap is milestone-based, not date-based. During testnet, tokens are real and carry over to mainnet — there is no reset.",
    },
    {
      question: "What is Proof of Infrastructure?",
      answer: "Nodes running the hardened OramaOS operating system receive a 1.5x multiplier to their Effective Power, verified via TPM-based remote attestation. Combined with Proof of Contribution (real work: uptime, compute, bandwidth) and Proof of Stake, this ensures a node doing real work outearns a whale who only stakes capital.",
    },
    {
      question: "What are Angels?",
      answer: "Angels are autonomous AI agents that run on the Orama Network. They can interact with on-chain primitives (SQL, KV, IPFS, BTC bridge, DEX), hold $ORAMA, execute transactions, and interact with other Angels. Builders deploy them to the AI Marketplace and set per-request or subscription pricing in $ORAMA.",
    },
    {
      question: "What about privacy?",
      answer: "Every transaction has a public/private toggle. Private transactions use PLONK zk-SNARKs to shield sender, receiver, and amount. Gas cost for private mode is 4x public. PLONK uses a universal trusted setup performed once at genesis — no new ceremony needed for circuit upgrades.",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader title="FAQ" subtitle="Common questions about the Orama L1." />
      </AnimateIn>

      <div className="flex flex-col gap-2 mt-8 max-w-3xl mx-auto">
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
                    <h3 className="font-display font-bold text-fg text-sm sm:text-base">{faq.question}</h3>
                    <ChevronRight className={`w-4 h-4 shrink-0 text-muted transition-transform duration-200 ${openIndex === i ? "rotate-90" : ""}`} />
                  </div>
                  <div className={`overflow-hidden transition-all duration-300 ${openIndex === i ? "max-h-60 mt-3 opacity-100" : "max-h-0 opacity-0"}`}>
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
   14. FINAL CTA
   ═══════════════════════════════════════════ */
function BlockchainCTA() {
  return (
    <Section padding="wide">
      <AnimateIn>
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col items-center text-center gap-6 py-8">
            <span
              className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full"
              style={{ border: `1px dashed ${SILVER.mid}`, color: SILVER.light }}
            >
              100% MINED — ZERO PRE-MINE
            </span>

            <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
              The eternal decentralized computer.
              <br />
              <span
                style={{
                  background: SILVER.gradient,
                  WebkitBackgroundClip: "text",
                  WebkitTextFillColor: "transparent",
                }}
              >
                Run a node. Mine $ORAMA.
              </span>
            </h2>

            <p className="text-muted text-sm max-w-lg">
              No tokens to buy beforehand. No pre-sale to miss. Run a node, earn $ORAMA,
              and be part of the only blockchain where everyone starts equal.
            </p>

            <div className="flex flex-wrap items-center gap-3 justify-center">
              <Button asChild variant="ghost" size="lg">
                <Link to="/node">
                  Run a Node
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </Link>
              </Button>
              <Button asChild variant="ghost" size="lg">
                <a href="/orama-whitepaper-v3.pdf" target="_blank" rel="noopener noreferrer">
                  Read Whitepaper
                  <ExternalLink className="w-3.5 h-3.5 ml-2" />
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
export default function Blockchain() {
  return (
    <Page title="Orama L1 Blockchain — The Eternal Decentralized Computer">
      <BlockchainHero />
      <Suspense fallback={null}>
        <ConsensusScene />
      </Suspense>
      <Section padding="none"><CrosshairDivider /></Section>
      <TwoLayers />
      <Section padding="none"><CrosshairDivider /></Section>
      <ThreePillars />
      <Section padding="none"><CrosshairDivider /></Section>
      <TokenomicsSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <AcquireSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <OramaOneBlockchain />
      <Section padding="none"><CrosshairDivider /></Section>
      <BtcBridgeAndDex />
      <Section padding="none"><CrosshairDivider /></Section>
      <PrivacySection />
      <Section padding="none"><CrosshairDivider /></Section>
      <AngelsSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <GovernanceSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <RootWalletSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <BlockchainRoadmap />
      <Section padding="none"><CrosshairDivider /></Section>
      <BlockchainFAQ />
      <Section padding="none"><CrosshairDivider /></Section>
      <BlockchainCTA />
    </Page>
  );
}
