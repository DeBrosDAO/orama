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
import { Redacted } from "../components/ui/redacted";
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
          ORAMA L1 BLOCKCHAIN
        </span>

        <h1 className="font-display font-bold text-4xl lg:text-6xl leading-tight">
          <SplitText
            text="Where AI meets"
            className="text-fg"
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
          />
          <br />
          <SplitText
            text="the blockchain."
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
          The Orama L1 is powered by Proof of Infrastructure — where nodes earn
          by doing real work, not just staking capital. An Orama One node with
          great uptime outranks a whale. Power to the people, not the wealthy.
        </p>

        {/* Caution */}
        <div
          className="flex items-center gap-3 px-4 py-3 rounded-lg max-w-md w-full"
          style={{ background: "rgba(234,179,8,0.06)", border: "1px solid rgba(234,179,8,0.2)" }}
        >
          <span className="w-2 h-2 rounded-full bg-yellow-500 shrink-0 animate-pulse-dot" />
          <span className="text-xs font-mono text-yellow-500/90 tracking-wider">
            $ORAMA is not yet released — token details will be announced soon
          </span>
        </div>

        <div className="flex flex-wrap items-center gap-3 justify-center pt-4">
          <span className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm text-black opacity-50 pointer-events-none">
            Coming Soon
          </span>
          <Button asChild variant="ghost" size="lg">
            <a href="/orama-whitepaper-v3.pdf" target="_blank" rel="noopener noreferrer">
              Whitepaper
              <ExternalLink className="w-3.5 h-3.5 ml-2" />
            </a>
          </Button>
        </div>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   2. THREE PILLARS
   ═══════════════════════════════════════════ */
function ThreePillars() {
  const pillars = [
    {
      icon: Server,
      title: "Proof of Infrastructure",
      description: "The primary consensus mechanism. Nodes earn by serving real compute, storage, and bandwidth. An Orama One node with great uptime outranks a whale who just stakes capital. Power to the people.",
      primary: true,
    },
    {
      icon: Coins,
      title: "Proof of Stake",
      description: "Secondary economic security layer. Validators stake $ORAMA as collateral. Slashing for malicious behavior. But staking alone doesn't make you dominant — real work does.",
      primary: false,
    },
    {
      icon: Cpu,
      title: "Proof of Angels",
      description: "AI agents monitor network health, detect threats, and flag suspicious behavior. An extra layer of intelligent security that evolves with the network.",
      primary: false,
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="What Makes It Different"
          subtitle="Three-layer consensus: infrastructure first, stake second, AI for security."
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
                    <span className="text-xs font-mono text-accent tracking-wider uppercase">Primary</span>
                  )}
                </div>
                <h3 className="font-display font-bold text-fg">{p.title}</h3>
                <p className="text-muted text-sm leading-relaxed">{p.description}</p>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   3. $ORAMA TOKEN
   ═══════════════════════════════════════════ */
/* TokenSection removed — merged into TokenomicsSection below */

/* ═══════════════════════════════════════════
   4. THE PRESALE
   ═══════════════════════════════════════════ */
function PresaleSection() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Pre-Sale"
          subtitle="Two ways to invest in the Orama Network before mainnet."
        />
      </AnimateIn>

      {/* Fundraise bar */}
      <AnimateIn>
        <div className="mt-8 mb-6">
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col gap-3 p-3">
              <div className="flex items-center justify-between">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">Total Fundraise</span>
                <span className="text-xs font-mono text-muted">
                  <Redacted /> BTC / <Redacted /> BTC
                </span>
              </div>
              <div className="h-3 bg-surface-2 rounded-full overflow-hidden border border-border">
                <div
                  className="h-full rounded-full transition-all duration-1000"
                  style={{ width: "2%", background: SILVER.gradient }}
                />
              </div>
              <div className="flex items-center justify-between text-xs font-mono text-muted">
                <div className="flex items-center gap-2">
                  <StatusDot status="active" />
                  <span>Pre-sale is open</span>
                </div>
                <span>Goal: Mainnet by 2028</span>
              </div>
            </div>
          </DashedPanel>
        </div>
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {/* Node License */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center justify-between">
                <div>
                  <span className="text-xs font-mono text-muted tracking-wider uppercase block">Node License</span>
                  <span className="font-display font-bold text-xl text-fg"><Redacted /> available</span>
                </div>
                <span className="font-display font-bold text-3xl text-fg">
                  <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>
                </span>
              </div>

              <p className="text-sm text-muted leading-relaxed">
                Purchase a license to operate an Orama node. Start earning
                $ORAMA rewards when the network scales.
              </p>

              <ul className="flex flex-col gap-2">
                {[
                  "Operate a node on the Orama Network",
                  "Earn $ORAMA rewards",
                  "Governance voting rights",
                  "Priority access at mainnet (2028)",
                  "Transferable — resell on secondary markets",
                ].map((item) => (
                  <li key={item} className="flex items-start gap-2">
                    <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                    <span className="text-xs text-muted">{item}</span>
                  </li>
                ))}
              </ul>

              <div className="flex items-center justify-between mt-auto pt-2">
                <div className="flex items-center gap-2">
                  <span className="text-xs font-mono text-muted">Pay with</span>
                  <span className="px-2 py-0.5 text-xs font-mono text-fg border border-border rounded">ETH</span>
                  <span className="px-2 py-0.5 text-xs font-mono text-fg border border-border rounded">SOL</span>
                </div>
                <span className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-4 py-1.5 text-xs rounded-sm text-black opacity-50 pointer-events-none">
                  Coming Soon
                </span>
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Token Pre-Sale */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center justify-between">
                <div>
                  <span className="text-xs font-mono text-muted tracking-wider uppercase block">Token Pre-Sale</span>
                  <span className="font-display font-bold text-xl text-fg"><Redacted /> $ORAMA</span>
                </div>
                <span className="font-display font-bold text-3xl text-fg">
                  <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>
                </span>
              </div>

              <p className="text-sm text-muted leading-relaxed">
                Buy $ORAMA before the public launch. Early investors get the
                lowest price — tokens are tradeable from day 1 at mainnet.
              </p>

              <ul className="flex flex-col gap-2">
                {[
                  <><Redacted /> tokens available at pre-sale price</>,
                  <><Redacted /> vesting with <Redacted /> cliff</>,
                  "Trade from day 1 at mainnet launch",
                  "Governance voting rights immediately",
                  "Stake for additional rewards",
                ].map((item, i) => (
                  <li key={i} className="flex items-start gap-2">
                    <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                    <span className="text-xs text-muted">{item}</span>
                  </li>
                ))}
              </ul>

              <div className="flex items-center justify-between mt-auto pt-2">
                <div className="flex items-center gap-2">
                  <span className="text-xs font-mono text-muted">Pay with</span>
                  <span className="px-2 py-0.5 text-xs font-mono text-fg border border-border rounded">ETH</span>
                  <span className="px-2 py-0.5 text-xs font-mono text-fg border border-border rounded">SOL</span>
                </div>
                <span className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-4 py-1.5 text-xs rounded-sm text-black opacity-50 pointer-events-none">
                  Coming Soon
                </span>
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   5. TOKENOMICS DEEP DIVE
   ═══════════════════════════════════════════ */
/* ═══════════════════════════════════════════
   5. ORAMA ONE — Hardware Node
   ═══════════════════════════════════════════ */
function OramaOneBlockchain() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Orama One"
          subtitle="The hardware node that powers Proof of Infrastructure."
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
                A purpose-built hardware node — the heart of the blockchain and the
                compute layer. Plug in, connect, and start validating through
                real infrastructure contribution.
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
              Why hardware matters
            </h3>

            <p className="text-muted text-sm leading-relaxed">
              Most blockchains are controlled by whoever has the most capital to stake.
              Orama flips this model.
              <span className="text-fg font-semibold"> An Orama One node with excellent uptime
              and real infrastructure contribution outranks a whale who just stakes tokens.</span>
            </p>

            <p className="text-muted text-sm leading-relaxed">
              This is Proof of Infrastructure — consensus power comes from doing real work,
              not from being rich. Running compute, serving storage, routing bandwidth.
              The Orama One is designed for exactly this: silent, efficient, 24/7 operation.
            </p>

            <ul className="flex flex-col gap-2">
              {[
                "Proof of Infrastructure — uptime and work beat capital",
                "Earn $ORAMA rewards",
                "Silent operation — designed for home or office",
                "Plug and earn — minimal setup, maximum contribution",
                "Each node strengthens the decentralized cloud",
              ].map((item) => (
                <li key={item} className="flex items-start gap-2">
                  <ChevronRight className="w-3.5 h-3.5 mt-0.5 shrink-0 text-accent" />
                  <span className="text-xs text-muted">{item}</span>
                </li>
              ))}
            </ul>

            <span className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-6 py-2.5 text-xs rounded-sm text-black w-fit opacity-50 pointer-events-none">
              Coming Soon
            </span>
          </div>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   6. TOKENOMICS
   ═══════════════════════════════════════════ */
function TokenomicsSection() {
  const utilities = [
    { icon: Zap, title: "Gas Currency", desc: "All transactions require $ORAMA. Fees dynamically adjusted." },
    { icon: Coins, title: "Staking", desc: "Validators and Angel operators stake $ORAMA for consensus and rewards." },
    { icon: Vote, title: "Governance", desc: "Vote on protocol upgrades, parameters, and treasury allocations." },
    { icon: Wallet, title: "Service Payments", desc: "Storage, compute, AI inference — all paid in $ORAMA." },
  ];

  const allocations = [
    { label: "Node Operators & Staking", pct: null, tokens: null, color: SILVER.light, schedule: null },
    { label: "Liquidity & DEX", pct: null, tokens: null, color: SILVER.mid, schedule: null },
    { label: "Treasury (DAO)", pct: null, tokens: null, color: SILVER.dark, schedule: null },
    { label: "Pre-Sale", pct: null, tokens: null, color: "#71717a", schedule: null },
    { label: "Core Team", pct: null, tokens: null, color: "#52525b", schedule: null },
    { label: "Marketing & Growth", pct: null, tokens: null, color: "#3f3f46", schedule: null },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Tokenomics"
          subtitle="$ORAMA — the native currency of the Orama blockchain."
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
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Total Supply</span>
                  <span className="font-display font-bold text-fg"><Redacted /> total supply</span>
                </div>
              </DashedPanel>
              <DashedPanel withBackground>
                <div className="flex flex-col gap-1 p-2">
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Pre-Sale</span>
                  <span className="font-display font-bold text-fg"><Redacted /> <span style={{ color: "#F7931A" }}>BTC</span></span>
                </div>
              </DashedPanel>
              <DashedPanel withCorners withBackground className="border-accent/30">
                <div className="flex flex-col gap-1 p-2">
                  <span className="text-xs font-mono text-accent tracking-wider uppercase">Launch Price</span>
                  <span className="font-display font-bold text-fg">
                    <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>
                  </span>
                </div>
              </DashedPanel>
            </div>

            {/* Fund allocation breakdown */}
            <DashedPanel withBackground>
              <div className="flex flex-col gap-3 p-2">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">Where the funds go</span>
                <div className="flex h-3 rounded-full overflow-hidden">
                  <div className="h-full" style={{ width: "73.5%", background: SILVER.light }} title="Initial LP" />
                  <div className="h-full" style={{ width: "25%", background: SILVER.dark }} title="Core Team" />
                  <div className="h-full" style={{ width: "1.5%", background: SILVER.mid }} title="Buffer" />
                </div>
                <div className="flex items-center justify-between text-xs font-mono text-muted">
                  <div className="flex items-center gap-1.5">
                    <span className="w-2 h-2 rounded-sm" style={{ background: SILVER.light }} />
                    <span>LP Seeding (<Redacted /> at <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>)</span>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <span className="w-2 h-2 rounded-sm" style={{ background: SILVER.dark }} />
                    <span><Redacted /> <span style={{ color: "#F7931A" }}>BTC</span> → Core Team</span>
                  </div>
                </div>
              </div>
            </DashedPanel>

            <p className="text-muted text-sm leading-relaxed">
              $ORAMA is the native gas currency. All transactions require it.
              while staking rewards and service revenue sustain the ecosystem.
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

      {/* Allocation + vesting */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8">
        {/* Allocation chart */}
        <AnimateIn>
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col gap-6 p-4">
              {/* Bar */}
              <div className="flex h-5 rounded-full overflow-hidden">
                {allocations.map((a) => (
                  <div
                    key={a.label}
                    className="h-full transition-all duration-500"
                    style={{ width: `${100 / allocations.length}%`, background: a.color }}
                    title={a.label}
                  />
                ))}
              </div>

              {/* Legend */}
              <div className="flex flex-col gap-3">
                {allocations.map((a) => (
                  <div key={a.label} className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="w-3 h-3 rounded-sm shrink-0" style={{ background: a.color }} />
                      <span className="text-xs font-mono text-muted">{a.label}</span>
                    </div>
                    <span className="text-xs font-mono text-fg"><Redacted /></span>
                  </div>
                ))}
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Vesting table */}
        <AnimateIn>
          <DashedPanel withBackground>
            <div className="overflow-x-auto p-2">
              <table className="w-full text-left text-xs sm:text-sm">
                <thead>
                  <tr className="border-b border-dashed border-border">
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Allocation</th>
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Tokens</th>
                    <th className="text-xs font-mono text-muted tracking-wider uppercase py-3">Vesting</th>
                  </tr>
                </thead>
                <tbody>
                  {allocations.map((row) => (
                    <tr key={row.label} className="border-b border-border/50">
                      <td className="text-sm text-fg py-3 pr-4">{row.label}</td>
                      <td className="text-sm text-fg py-3 pr-4 font-mono"><Redacted /></td>
                      <td className="text-sm text-muted py-3"><Redacted /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>

      {/* Pre-sale CTA */}
      <AnimateIn>
        <div className="flex flex-wrap items-center justify-center gap-3 mt-8">
          <span className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm text-black opacity-50 pointer-events-none">
            Coming Soon
          </span>
          <Button asChild variant="ghost" size="lg">
            <Link to="/investors">
              View Full Tokenomics
              <ArrowRight className="w-3.5 h-3.5 ml-2" />
            </Link>
          </Button>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   6. NETWORK ROADMAP
   ═══════════════════════════════════════════ */
function BlockchainRoadmap() {
  const phases = [
    {
      status: "live" as const,
      label: "NOW",
      title: "Devnet",
      items: [
        "50+ nodes running across devnet and testnet",
        "Node license and token pre-sale open",
        "Core infrastructure operational (SQL, Cache, Storage, DNS)",
        "AnChat Lite in closed beta",
        "Orama Proxy integrated",
      ],
    },
    {
      status: "building" as const,
      label: "2025-2026",
      title: "Testnet",
      items: [
        "L1 blockchain testnet deployment",
        "Proof of Infrastructure consensus testing",
        "AI agent (Angels) layer development",
        "RootWallet integration",
        "Developer waitlist opens for deployment",
      ],
    },
    {
      status: "planned" as const,
      label: "2027",
      title: "Scale",
      items: [
        "Node license holders become active operators",
        "Operators start earning $ORAMA rewards",
        "Launchpad and DEX go live on testnet",
        "Zero-knowledge transaction layer",
        "Enterprise partnerships",
      ],
    },
    {
      status: "planned" as const,
      label: "2028",
      title: "Mainnet",
      items: [
        "Full mainnet launch — L1 blockchain live",
        "Pre-sale token holders can trade from day 1",
        "Complete decentralization — no central authority",
        "Cross-chain bridges (ETH, SOL)",
        "The decentralized cloud, fully operational",
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
          title="Blockchain Roadmap"
          subtitle="From devnet to mainnet — every step mapped out."
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
   7. BUILT-IN DEFI
   ═══════════════════════════════════════════ */
/* ═══════════════════════════════════════════
   ANGELS MARKETPLACE
   ═══════════════════════════════════════════ */
function AngelsSection() {
  const steps = [
    { number: "01", title: "Build", desc: "Create an AI agent in any language" },
    { number: "02", title: "Deploy", desc: "Push it to the Orama Network" },
    { number: "03", title: "Earn", desc: "Get paid in $ORAMA every time it's used" },
  ];

  const useCases = [
    { icon: Cpu, title: "Monitoring", desc: "Network health & threat detection" },
    { icon: Zap, title: "Automation", desc: "Event-driven tasks & workflows" },
    { icon: Wallet, title: "Trading", desc: "On-chain trading strategies" },
    { icon: Server, title: "AI Inference", desc: "Model hosting & API serving" },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Angels"
          subtitle="AI agents with their own wallets. Deploy yours. Get paid when others use it."
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
                <h3 className="font-display font-bold text-fg">Angel Marketplace</h3>
                <span className="text-xs font-mono text-muted/50 tracking-wider">COMING SOON</span>
              </div>

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
                Browse, discover, and use AI agents built by the community. Pay per use or subscribe.
              </p>
            </div>
          </DashedPanel>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   MULTI-CHAIN
   ═══════════════════════════════════════════ */
function MultiChainSection() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Multi-Chain"
          subtitle="Buy $ORAMA with any major crypto. One wallet for everything."
        />
      </AnimateIn>

      {/* Chain cards */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mt-8">
        {/* Orama */}
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col items-center text-center gap-3 p-4">
              <span
                className="w-12 h-12 flex items-center justify-center rounded-full text-lg font-display font-bold"
                style={{ background: SILVER.gradient, color: "#000" }}
              >
                O
              </span>
              <span className="font-display font-bold text-sm text-fg">Orama</span>
              <span className="text-xs font-mono text-muted">NATIVE</span>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Bitcoin */}
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col items-center text-center gap-3 p-4">
              <span className="w-12 h-12 flex items-center justify-center rounded-full" style={{ border: "1px solid #F7931A40" }}>
                <svg viewBox="0 0 32 32" className="w-6 h-6" fill="none">
                  <path d="M23.189 14.02c.314-2.096-1.283-3.223-3.465-3.975l.708-2.84-1.728-.43-.69 2.765c-.454-.113-.92-.22-1.385-.326l.695-2.783L15.596 6l-.708 2.839c-.376-.086-.745-.171-1.104-.261l.002-.009-2.384-.595-.46 1.846s1.283.294 1.256.312c.7.175.827.638.806 1.006l-.807 3.238c.048.012.11.03.179.058l-.182-.045-1.132 4.542c-.086.213-.304.532-.793.411.018.025-1.256-.313-1.256-.313L8 20.728l2.25.561c.418.105.828.215 1.231.318l-.715 2.872 1.727.43.709-2.842c.472.128.93.246 1.378.357l-.706 2.828 1.728.43.715-2.866c2.948.558 5.164.333 6.097-2.333.752-2.146-.037-3.385-1.588-4.192 1.13-.26 1.979-1.003 2.207-2.538zm-3.95 5.538c-.533 2.147-4.148.986-5.32.695l.95-3.805c1.172.292 4.929.872 4.37 3.11zm.535-5.569c-.487 1.953-3.495.96-4.47.717l.86-3.45c.975.243 4.118.696 3.61 2.733z" fill="#F7931A" />
                </svg>
              </span>
              <span className="font-display font-bold text-sm" style={{ color: "#F7931A" }}>Bitcoin</span>
              <span className="text-xs font-mono text-muted">BTC</span>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Ethereum */}
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col items-center text-center gap-3 p-4">
              <span className="w-12 h-12 flex items-center justify-center rounded-full" style={{ border: `1px solid ${SILVER.border}` }}>
                <svg viewBox="0 0 256 417" className="w-5 h-8" fill="none">
                  <path d="M127.961 0l-2.795 9.5v275.668l2.795 2.79 127.962-75.638z" fill="#a1a1aa" />
                  <path d="M127.962 0L0 212.32l127.962 75.639V154.158z" fill="#d4d4d8" />
                  <path d="M127.961 312.187l-1.575 1.92V414.55l1.575 4.6 128.038-180.32z" fill="#a1a1aa" />
                  <path d="M127.962 419.15V312.187L0 238.83z" fill="#d4d4d8" />
                </svg>
              </span>
              <span className="font-display font-bold text-sm text-fg">Ethereum</span>
              <span className="text-xs font-mono text-muted">ETH</span>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Solana */}
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col items-center text-center gap-3 p-4">
              <span className="w-12 h-12 flex items-center justify-center rounded-full" style={{ border: `1px solid ${SILVER.border}` }}>
                <svg viewBox="0 0 397 312" className="w-6 h-5" fill="none">
                  <path d="M64.6 237.9c2.4-2.4 5.7-3.8 9.2-3.8h317.4c5.8 0 8.7 7 4.6 11.1l-62.7 62.7c-2.4 2.4-5.7 3.8-9.2 3.8H6.5c-5.8 0-8.7-7-4.6-11.1l62.7-62.7z" fill="#d4d4d8" />
                  <path d="M64.6 3.8C67.1 1.4 70.4 0 73.8 0h317.4c5.8 0 8.7 7 4.6 11.1l-62.7 62.7c-2.4 2.4-5.7 3.8-9.2 3.8H6.5c-5.8 0-8.7-7-4.6-11.1L64.6 3.8z" fill="#d4d4d8" />
                  <path d="M333.1 120c-2.4-2.4-5.7-3.8-9.2-3.8H6.5c-5.8 0-8.7 7-4.6 11.1l62.7 62.7c2.4 2.4 5.7 3.8 9.2 3.8h317.4c5.8 0 8.7-7 4.6-11.1L333.1 120z" fill="#a1a1aa" />
                </svg>
              </span>
              <span className="font-display font-bold text-sm text-fg">Solana</span>
              <span className="text-xs font-mono text-muted">SOL</span>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>

      {/* RootWallet + Open Source */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-6">
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
                One Orama address. Multiple chain accounts underneath — BTC, ETH, SOL.
                Single identity across everything.
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
                Orama Network is fully open source. Verify every line of code.
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
   BUILT-IN DEFI
   ═══════════════════════════════════════════ */
function DeFiSection() {
  const features = [
    {
      icon: Repeat,
      title: "Native DEX / Swap",
      description: "Swap any token on the Orama L1 — no bridges, no wrapping. Native liquidity pools with low fees.",
      status: "Testnet 2027",
    },
    {
      icon: Zap,
      title: "Launchpad",
      description: "Launch tokens directly on the Orama L1. Built-in bonding curves, fair launch mechanics, and instant liquidity.",
      status: "Testnet 2027",
    },
    {
      icon: Wallet,
      title: "RootWallet",
      description: "Seamless wallet onboarding — no seed phrases for end users. Social login, biometrics, and account abstraction built in.",
      status: "In Development",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Built-in DeFi"
          subtitle="Native financial infrastructure — not bolted on, built in."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-8">
        {features.map((f) => (
          <AnimateIn key={f.title}>
            <DashedPanel withBackground className="h-full">
              <div className="flex flex-col gap-4 p-3">
                <div className="flex items-center justify-between">
                  <f.icon className="w-5 h-5 text-accent" />
                  <span className="text-xs font-mono text-muted/50 tracking-wider">{f.status}</span>
                </div>
                <h3 className="font-display font-bold text-fg">{f.title}</h3>
                <p className="text-muted text-sm leading-relaxed">{f.description}</p>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   8. TECHNICAL SPECS
   ═══════════════════════════════════════════ */
/* TechSpecs removed — will be in the whitepaper */

/* ═══════════════════════════════════════════
   9. FAQ
   ═══════════════════════════════════════════ */
function BlockchainFAQ() {
  const [openIndex, setOpenIndex] = useState<number | null>(null);

  const faqs = [
    {
      question: "When is mainnet launching?",
      answer: "Mainnet is targeted for 2028. The network is currently on devnet with 50+ nodes. Testnet will follow in 2025-2026, with the scaling phase in 2027 where operators begin earning rewards.",
    },
    {
      question: "What chain is this built on?",
      answer: "Orama is its own L1 blockchain — not a layer 2, not a fork. It's EVM-compatible so developers can use familiar tooling, but the consensus mechanism (Proof of Infrastructure + Proof of Angels) is entirely custom.",
    },
    {
      question: "How do I buy $ORAMA?",
      answer: "During pre-sale, you can purchase $ORAMA tokens. After mainnet launch, $ORAMA will be available on the native Orama DEX and potentially external exchanges.",
    },
    {
      question: "What's the vesting schedule for pre-sale tokens?",
      answer: "Pre-sale tokens have a vesting period with a cliff. After mainnet launches, tokens become tradeable from day 1. Node license holders can start earning rewards during the scaling phase (2027).",
    },
    {
      question: "What's the difference between a node license and buying tokens?",
      answer: "A node license gives you the right to operate a node and earn ongoing rewards from serving compute, storage, and bandwidth. Buying tokens is a direct investment in $ORAMA — pre-sale investors get a discount vs the launch price. You can do both. All payments accepted in BTC.",
    },
    {
      question: "What is Proof of Infrastructure?",
      answer: "Proof of Infrastructure (PoI) is Orama's primary consensus mechanism. Unlike traditional Proof of Stake where the richest validators dominate, PoI rewards nodes for real work — uptime, compute served, storage provided, bandwidth contributed. An Orama One hardware node with excellent uptime outranks a whale who just stakes capital. PoS provides secondary economic security, and Proof of Angels (AI monitoring) adds an extra intelligent security layer.",
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
   10. FINAL CTA
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
              PRE-SALE IS OPEN
            </span>

            <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
              The L1 for infrastructure.
              <br />
              <span
                style={{
                  background: SILVER.gradient,
                  WebkitBackgroundClip: "text",
                  WebkitTextFillColor: "transparent",
                }}
              >
                Get in early.
              </span>
            </h2>

            <div className="flex flex-wrap items-center gap-3 justify-center">
              <span className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm text-black opacity-50 pointer-events-none">
                Coming Soon
              </span>
              <span className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm text-black opacity-50 pointer-events-none">
                Coming Soon
              </span>
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
    <Page title="Orama L1 Blockchain — Token, Presale & Tokenomics">
      <BlockchainHero />
      <Suspense fallback={null}>
        <ConsensusScene />
      </Suspense>
      <Section padding="none"><CrosshairDivider /></Section>
      <ThreePillars />
      <Section padding="none"><CrosshairDivider /></Section>
      <TokenomicsSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <PresaleSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <OramaOneBlockchain />
      <Section padding="none"><CrosshairDivider /></Section>
      <AngelsSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <MultiChainSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <BlockchainRoadmap />
      <Section padding="none"><CrosshairDivider /></Section>
      <DeFiSection />
      <Section padding="none"><CrosshairDivider /></Section>
      <BlockchainFAQ />
      <Section padding="none"><CrosshairDivider /></Section>
      <BlockchainCTA />
    </Page>
  );
}
