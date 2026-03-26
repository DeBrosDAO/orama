import { useState, lazy, Suspense } from "react";
import { Link } from "react-router";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { Badge } from "../components/ui/badge";
import { DashedPanel } from "../components/ui/dashed-panel";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";
import { StatusDot } from "../components/ui/status-dot";
import { Button } from "../components/ui/button";
import { SpecTable } from "../components/ui/spec-table";
import { SplitText } from "../components/ui/split-text";
import { SILVER } from "../components/ui/silver-theme";
import { SponsorsShowcase } from "../components/landing/sponsors-showcase";

const GrowthVaultScene = lazy(() =>
  import("../components/landing/growth-vault-scene").then((m) => ({
    default: m.GrowthVaultScene,
  })),
);
import {
  ArrowRight,
  Server,
  Shield,
  Coins,
  Wallet,
  Lock,
  Vote,
  ExternalLink,
  TrendingUp,
  Globe,
  Zap,
  ChevronRight,
  Target,
  DollarSign,
  Clock,
  Award,
  Code,
} from "lucide-react";
import oramaIcon from "../assets/orama-icon.png";
import { Redacted } from "../components/ui/redacted";

/* ═══════════════════════════════════════════
   1. HERO — Hook with dual investment paths
   ═══════════════════════════════════════════ */
function InvestorHero() {
  return (
    <Section padding="wide">
      <div className="investors-hero flex flex-col items-center text-center min-h-[70vh] pt-[12vh] gap-6 max-w-3xl mx-auto">
        <span
          className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full"
          style={{ border: `1px dashed ${SILVER.mid}`, color: SILVER.light }}
        >
          FOR INVESTORS
        </span>

        <h1 className="font-display font-bold text-4xl lg:text-6xl leading-tight">
          <SplitText
            text="The future runs on Orama."
            className="text-fg"
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
          />
          <br />
          <SplitText
            text="Be the one who funded it."
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
            className=""
          />
        </h1>

        <style>{`
          .investors-hero h1 > span:last-of-type .split-char {
            background: ${SILVER.gradient};
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
          }
        `}</style>

        <p className="text-muted text-sm leading-relaxed max-w-lg">
          We're raising <Redacted /> BTC to bring Orama Network to mainnet —
          <Redacted /> from node licenses and <Redacted /> from
          a token pre-sale. Paid in BTC.
        </p>

        {/* Fundraise progress */}
        <div className="w-full max-w-md">
          <div className="flex items-center justify-between text-xs font-mono text-muted mb-1">
            <span><Redacted /> <span style={{ color: "#F7931A" }}>BTC</span> raised</span>
            <span><Redacted /> <span style={{ color: "#F7931A" }}>BTC</span> goal</span>
          </div>
          <div className="h-3 bg-surface-2 rounded-full overflow-hidden border border-border">
            <div
              className="h-full rounded-full transition-all duration-1000"
              style={{
                width: "2%",
                background: SILVER.gradient,
              }}
            />
          </div>
          <div className="flex items-center justify-center gap-2 mt-2">
            <StatusDot status="active" />
            <span className="text-xs font-mono text-muted">
              Fundraise in progress
            </span>
          </div>
        </div>

        {/* Dual CTA */}
        <div className="flex flex-wrap items-center gap-3 justify-center pt-2">
          <a href="#invest" className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm cursor-pointer text-black">
            View Investment Options
            <ArrowRight className="w-3.5 h-3.5 ml-2" />
          </a>
          <Button asChild variant="ghost" size="lg">
            <a
              href="/orama-whitepaper-v3.pdf"
              target="_blank"
              rel="noopener noreferrer"
            >
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
   2. THE OPPORTUNITY — Why Orama, why now
   ═══════════════════════════════════════════ */
function TheOpportunity() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="The Opportunity"
          subtitle="Cloud infrastructure is a $600B market controlled by three companies. We're building the alternative."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-8">
        {[
          {
            icon: Globe,
            title: "Centralized cloud is broken",
            desc: "AWS, GCP, and Azure control 67% of the cloud market. Single points of failure, vendor lock-in, and rising costs. Developers deserve better.",
          },
          {
            icon: Zap,
            title: "Decentralized compute is inevitable",
            desc: "Edge computing, AI workloads, and data sovereignty laws are pushing compute away from centralized data centers. The infrastructure needs to follow.",
          },
          {
            icon: TrendingUp,
            title: "Early-stage with real product",
            desc: "Orama Network isn't a whitepaper project. We have 50+ nodes running across 2 environments, a working CLI, container deployments, DNS, databases, and WASM functions — all live today.",
          },
        ].map((item) => (
          <AnimateIn key={item.title}>
            <DashedPanel withBackground className="h-full">
              <div className="flex flex-col gap-3 p-2">
                <item.icon className="w-5 h-5 text-accent" />
                <h3 className="font-display font-bold text-fg">
                  {item.title}
                </h3>
                <p className="text-sm text-muted leading-relaxed">
                  {item.desc}
                </p>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>

      {/* Key metrics */}
      <AnimateIn>
        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-4 mt-8">
          {[
            { label: "Nodes Live", value: "50+" },
            { label: "Environments", value: "3" },
            { label: "Privacy Layer", value: "Orama Proxy" },
            { label: "Target Mainnet", value: "2028" },
          ].map((m) => (
            <DashedPanel key={m.label} className="p-4">
              <div className="flex flex-col gap-1">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">
                  {m.label}
                </span>
                <span
                  className="text-lg font-bold tabular-nums tracking-tight"
                  style={{
                    background: SILVER.gradient,
                    WebkitBackgroundClip: "text",
                    WebkitTextFillColor: "transparent",
                  }}
                >
                  {m.value}
                </span>
              </div>
            </DashedPanel>
          ))}
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   3. TWO WAYS TO INVEST — Side by side
   ═══════════════════════════════════════════ */
function TwoWaysToInvest() {
  return (
    <Section id="invest">
      <AnimateIn>
        <SectionHeader
          title="Two Ways to Invest"
          subtitle="Choose the investment that matches your involvement level."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8 mt-8">
        {/* Node License */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-6 p-4">
              <div className="flex items-center gap-4">
                <div className="w-14 h-14 shrink-0 flex items-center justify-center bg-white/[0.03] border border-border rounded-lg p-2">
                  <img
                    src={oramaIcon}
                    alt="Orama"
                    className="w-full h-full object-contain"
                  />
                </div>
                <div>
                  <h3 className="font-display font-bold text-xl text-fg">
                    Node License
                  </h3>
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">
                    Operate & Earn
                  </span>
                </div>
              </div>

              <div className="flex items-center justify-between py-4 border-t border-b border-dashed border-border">
                <span className="text-sm font-mono text-muted">Price</span>
                <span className="text-3xl font-display font-bold text-fg">
                  <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>
                </span>
              </div>

              <div className="flex flex-col gap-3">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">
                  What you get
                </span>
                {[
                  { icon: Server, text: "Right to operate an Orama Network node" },
                  { icon: Coins, text: "Earn $ORAMA for every request served" },
                  { icon: Shield, text: "Built-in Orama Proxy privacy relay" },
                  { icon: Vote, text: "Governance voting rights" },
                  { icon: Lock, text: "Priority mainnet access (2028)" },
                  { icon: Wallet, text: "Transferable — resellable on secondary markets" },
                ].map((item) => (
                  <div key={item.text} className="flex items-start gap-3">
                    <item.icon className="w-4 h-4 mt-0.5 shrink-0 text-accent" />
                    <span className="text-sm text-muted">{item.text}</span>
                  </div>
                ))}
              </div>

              <div className="flex items-center gap-2 pt-2">
                <span className="text-xs font-mono text-muted">Pay with</span>
                {["BTC"].map((c) => (
                  <span
                    key={c}
                    className="px-2 py-0.5 text-xs font-mono font-bold border border-border rounded" style={{ color: "#F7931A", borderColor: "#F7931A40" }}
                  >
                    {c}
                  </span>
                ))}
              </div>

              <div className="flex items-center justify-between text-xs font-mono text-muted border-t border-dashed border-border pt-4">
                <span><Redacted /> licenses available</span>
                <span><Redacted /> remaining</span>
              </div>

              <button disabled className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm text-black w-full opacity-50 cursor-not-allowed">
                Coming Soon
              </button>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Token Pre-Sale */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-6 p-4">
              <div className="flex items-center gap-4">
                <div className="w-14 h-14 shrink-0 flex items-center justify-center bg-white/[0.03] border border-border rounded-lg p-2">
                  <Coins className="w-8 h-8 text-accent" />
                </div>
                <div>
                  <h3 className="font-display font-bold text-xl text-fg">
                    Token Pre-Sale
                  </h3>
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">
                    Buy $ORAMA Early
                  </span>
                </div>
              </div>

              <div className="flex items-center justify-between py-4 border-t border-b border-dashed border-border">
                <span className="text-sm font-mono text-muted">
                  Pre-Sale Price
                </span>
                <div className="text-right">
                  <span className="text-3xl font-display font-bold text-fg">
                    <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>
                  </span>
                  <div className="text-xs font-mono text-muted mt-1">
                    Launch price: <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>
                  </div>
                </div>
              </div>

              <div className="flex flex-col gap-3">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">
                  What you get
                </span>
                {[
                  { icon: TrendingUp, text: "Upside at launch — details coming soon" },
                  { icon: Coins, text: "Tokens available at pre-sale price — details coming soon" },
                  { icon: Vote, text: "Governance voting rights" },
                  { icon: Wallet, text: "Stake tokens to earn rewards" },
                  { icon: Clock, text: "Vesting schedule — details coming soon" },
                  { icon: Lock, text: "Tradeable from day 1 after vesting" },
                ].map((item) => (
                  <div key={item.text} className="flex items-start gap-3">
                    <item.icon className="w-4 h-4 mt-0.5 shrink-0 text-accent" />
                    <span className="text-sm text-muted">{item.text}</span>
                  </div>
                ))}
              </div>

              <div className="flex items-center gap-2 pt-2">
                <span className="text-xs font-mono text-muted">Pay with</span>
                {["BTC"].map((c) => (
                  <span
                    key={c}
                    className="px-2 py-0.5 text-xs font-mono font-bold border border-border rounded" style={{ color: "#F7931A", borderColor: "#F7931A40" }}
                  >
                    {c}
                  </span>
                ))}
              </div>

              <div className="flex items-center justify-between text-xs font-mono text-muted border-t border-dashed border-border pt-4">
                <span><Redacted /> tokens at <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span></span>
                <span><Redacted /> <span style={{ color: "#F7931A" }}>BTC</span> total raise</span>
              </div>

              <button disabled className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm text-black w-full opacity-50 cursor-not-allowed">
                Coming Soon
              </button>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>

      {/* Quick comparison */}
      <AnimateIn>
        <div className="mt-8">
          <SpecTable
            rows={[
              { label: "Minimum Investment", value: "Details coming soon" },
              { label: "Earning Mechanism", value: "Node: $ORAMA rewards · Token: Staking + appreciation" },
              { label: "Involvement", value: "Node: Run infrastructure · Token: Passive" },
              { label: "Supply", value: "Details coming soon" },
              { label: "Vesting", value: "Details coming soon" },
            ]}
          />
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   4. DEBROS NFT HOLDERS — Free license callout
   ═══════════════════════════════════════════ */
function DebrosNftCallout() {
  return (
    <Section>
      <AnimateIn>
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col md:flex-row items-center gap-6 p-4">
            <div className="w-16 h-16 shrink-0 flex items-center justify-center rounded-lg border border-dashed border-accent/30 bg-accent/[0.05]">
              <Award className="w-8 h-8 text-accent" />
            </div>
            <div className="flex-1 text-center md:text-left">
              <h3 className="font-display font-bold text-lg text-fg">
                DeBros NFT Holders Get a Free Node License
              </h3>
              <p className="text-sm text-muted mt-2 leading-relaxed max-w-xl">
                Already hold a DeBros Team NFT? You're entitled to a free Orama
                Network node license — no purchase required. Connect your wallet
                to claim yours and start earning $ORAMA rewards from day one.
              </p>
            </div>
            <Button asChild size="lg">
              <Link to="/invest" className="shrink-0">
                Claim License
                <ArrowRight className="w-3.5 h-3.5 ml-2" />
              </Link>
            </Button>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   4b. $ANCHAT HOLDER BONUS
   ═══════════════════════════════════════════ */
function AnchatHolderCallout() {
  return (
    <Section>
      <AnimateIn>
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col md:flex-row items-center gap-6 p-4">
            <div className="w-16 h-16 shrink-0 flex items-center justify-center rounded-lg border border-dashed border-accent/30 bg-accent/[0.05]">
              <Coins className="w-8 h-8 text-accent" />
            </div>
            <div className="flex-1 text-center md:text-left">
              <h3 className="font-display font-bold text-lg text-fg">
                $ANCHAT Holders Get Free $ORAMA
              </h3>
              <p className="text-sm text-muted mt-2 leading-relaxed max-w-xl">
                Hold $ANCHAT tokens from anchat.io? You're eligible to claim a portion
                of your holdings as $ORAMA tokens. Conversion rate and claim details
                coming soon. Connect your Solana wallet to check your balance
                and claim.
              </p>
            </div>
            <Button asChild size="lg">
              <Link to="/invest" className="shrink-0">
                Claim $ORAMA
                <ArrowRight className="w-3.5 h-3.5 ml-2" />
              </Link>
            </Button>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   5. WHERE THE MONEY GOES — Fund allocation
   ═══════════════════════════════════════════ */
function FundAllocation() {
  const allocations = [
    {
      label: "Initial Liquidity Pool",
      amount: "***",
      pct: 20,
      desc: "Seeding the $ORAMA liquidity pool. Deep liquidity from day 1 so holders can trade freely.",
      color: SILVER.light,
    },
    {
      label: "Core Development",
      amount: "***",
      pct: 20,
      desc: "Engineering team salaries, infrastructure costs, security audits, and protocol development through mainnet launch.",
      color: SILVER.mid,
    },
    {
      label: "Marketing & Growth",
      amount: "***",
      pct: 20,
      desc: "Developer relations, community building, partnerships, conference presence, and ecosystem grants.",
      color: SILVER.dark,
    },
    {
      label: "Legal & Compliance",
      amount: "***",
      pct: 20,
      desc: "Token legal structure, regulatory compliance, entity setup, and ongoing counsel.",
      color: "#52525b",
    },
    {
      label: "Reserve",
      amount: "***",
      pct: 20,
      desc: "Emergency fund for unexpected costs, security incidents, or strategic opportunities.",
      color: "#3f3f46",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Where the Money Goes"
          subtitle="Full transparency on fund allocation — details coming soon."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8 mt-8">
        {/* Visual breakdown */}
        <AnimateIn>
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col gap-6 p-4">
              <div className="flex items-center justify-between">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">
                  Total Raise
                </span>
                <span className="font-display font-bold text-2xl text-fg">
                  <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>
                </span>
              </div>

              {/* Horizontal bar */}
              <div className="flex h-5 rounded-full overflow-hidden">
                {allocations.map((a) => (
                  <div
                    key={a.label}
                    className="h-full transition-all duration-500"
                    style={{ width: `${a.pct}%`, background: a.color }}
                    title={`${a.label}: ${a.pct}%`}
                  />
                ))}
              </div>

              {/* Legend */}
              <div className="flex flex-col gap-3">
                {allocations.map((a) => (
                  <div
                    key={a.label}
                    className="flex items-center justify-between"
                  >
                    <div className="flex items-center gap-2">
                      <span
                        className="w-3 h-3 rounded-sm shrink-0"
                        style={{ background: a.color }}
                      />
                      <span className="text-xs font-mono text-muted">
                        {a.label}
                      </span>
                    </div>
                    <div className="flex items-center gap-3">
                      <span className="text-xs font-mono text-fg">
                        {a.amount}
                      </span>
                      <span className="text-xs font-mono text-muted w-10 text-right">
                        <Redacted />
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Detailed descriptions */}
        <AnimateIn>
          <div className="flex flex-col gap-4">
            {allocations.map((a) => (
              <div
                key={a.label}
                className="border-l-2 pl-4 py-1"
                style={{ borderColor: a.color }}
              >
                <div className="flex items-center justify-between">
                  <h4 className="font-display font-bold text-sm text-fg">
                    {a.label}
                  </h4>
                  <span className="text-xs font-mono text-muted">
                    {a.amount}
                  </span>
                </div>
                <p className="text-xs text-muted leading-relaxed mt-1">
                  {a.desc}
                </p>
              </div>
            ))}
          </div>
        </AnimateIn>
      </div>

      {/* Key callout */}
      <AnimateIn>
        <div
          className="flex items-center gap-3 px-4 py-3 rounded-lg mt-8 max-w-2xl mx-auto"
          style={{
            background: "rgba(161,161,170,0.06)",
            border: `1px solid ${SILVER.border}`,
          }}
        >
          <Target className="w-4 h-4 shrink-0 text-accent" />
          <span className="text-xs font-mono text-muted">
            A significant portion of raised funds will go directly into liquidity — ensuring strong
            market depth and tradability from launch day. Exact allocation coming soon.
          </span>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   6. TOKENOMICS DEEP DIVE
   ═══════════════════════════════════════════ */
function TokenomicsDeepDive() {
  const allocations = [
    { label: "Node Operators & Staking", pct: 16.7, tokens: "***", color: SILVER.light },
    { label: "Liquidity & DEX", pct: 16.7, tokens: "***", color: SILVER.mid },
    { label: "Treasury (DAO)", pct: 16.7, tokens: "***", color: SILVER.dark },
    { label: "Pre-Sale", pct: 16.7, tokens: "***", color: "#71717a" },
    { label: "Core Team", pct: 16.6, tokens: "***", color: "#52525b" },
    { label: "Marketing & Growth", pct: 16.6, tokens: "***", color: "#3f3f46" },
  ];

  const vestingSchedule = [
    { allocation: "Node Operators", pct: "***", amount: "***", schedule: "***", cliff: "***", unlock: "***" },
    { allocation: "Liquidity & DEX", pct: "***", amount: "***", schedule: "***", cliff: "***", unlock: "***" },
    { allocation: "Treasury (DAO)", pct: "***", amount: "***", schedule: "***", cliff: "***", unlock: "***" },
    { allocation: "Pre-Sale", pct: "***", amount: "***", schedule: "***", cliff: "***", unlock: "***" },
    { allocation: "Core Team", pct: "***", amount: "***", schedule: "***", cliff: "***", unlock: "***" },
    { allocation: "Marketing", pct: "***", amount: "***", schedule: "***", cliff: "***", unlock: "***" },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Tokenomics"
          subtitle="$ORAMA — Fixed supply. No inflation. No additional minting. Details coming soon."
        />
      </AnimateIn>

      {/* Supply overview */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8">
        <AnimateIn>
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col gap-6 p-4">
              <div className="flex items-center justify-between">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">
                  Total Supply
                </span>
                <span
                  className="font-display font-bold text-xl"
                  style={{
                    background: SILVER.gradient,
                    WebkitBackgroundClip: "text",
                    WebkitTextFillColor: "transparent",
                  }}
                >
                  <Redacted />
                </span>
              </div>

              {/* Bar chart */}
              <div className="flex h-4 rounded-full overflow-hidden">
                {allocations.map((a) => (
                  <div
                    key={a.label}
                    className="h-full transition-all duration-500"
                    style={{ width: `${a.pct}%`, background: a.color }}
                    title={`${a.label}: ${a.pct}%`}
                  />
                ))}
              </div>

              {/* Legend */}
              <div className="flex flex-col gap-2">
                {allocations.map((a) => (
                  <div
                    key={a.label}
                    className="flex items-center justify-between"
                  >
                    <div className="flex items-center gap-2">
                      <span
                        className="w-3 h-3 rounded-sm shrink-0"
                        style={{ background: a.color }}
                      />
                      <span className="text-xs font-mono text-muted">
                        {a.label}
                      </span>
                    </div>
                    <div className="flex items-center gap-3">
                      <span className="text-xs font-mono text-fg">
                        {a.tokens}
                      </span>
                      <span className="text-xs font-mono text-muted w-8 text-right">
                        <Redacted />
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Token utility */}
        <AnimateIn>
          <div className="flex flex-col gap-4">
            <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
              Token Utility
            </h3>
            {[
              {
                icon: Coins,
                title: "Staking & Rewards",
                desc: "Operators stake $ORAMA to run nodes. Rewards scale with uptime, bandwidth, and compute contribution. Higher stake = higher multiplier.",
              },
              {
                icon: Vote,
                title: "Governance",
                desc: "Token holders vote on network proposals, treasury allocation, and protocol upgrades. 1 token = 1 vote.",
              },
              {
                icon: DollarSign,
                title: "Payments",
                desc: "Developers pay for compute, storage, and bandwidth in $ORAMA. This creates constant buy pressure as the network grows.",
              },
              {
                icon: Shield,
                title: "Privacy Built In",
                desc: "Node operators earn $ORAMA tokens for every request served — with built-in privacy via Orama Proxy.",
              },
            ].map((u) => (
              <DashedPanel key={u.title} withBackground>
                <div className="flex gap-3 p-2">
                  <u.icon className="w-4 h-4 mt-0.5 shrink-0 text-accent" />
                  <div>
                    <h4 className="font-display font-bold text-sm text-fg">
                      {u.title}
                    </h4>
                    <p className="text-xs text-muted leading-relaxed mt-1">
                      {u.desc}
                    </p>
                  </div>
                </div>
              </DashedPanel>
            ))}
          </div>
        </AnimateIn>
      </div>

      {/* Launch mechanics */}
      <AnimateIn>
        <div className="mt-8">
          <DashedPanel withBackground>
            <div className="p-4">
              <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-4">
                Launch Mechanics
              </h3>
              <div className="grid grid-cols-1 sm:grid-cols-3 gap-6">
                <div className="flex flex-col gap-1">
                  <span className="text-xs font-mono text-muted">Pre-Sale Price</span>
                  <span className="text-xl font-bold font-mono text-fg"><Redacted /></span>
                  <span className="text-xs text-muted"><Redacted /> tokens available</span>
                </div>
                <div className="flex flex-col gap-1">
                  <span className="text-xs font-mono text-muted">LP Launch Price</span>
                  <span className="text-xl font-bold font-mono text-fg"><Redacted /></span>
                  <span className="text-xs text-muted"><Redacted /> seeded into LP</span>
                </div>
                <div className="flex flex-col gap-1">
                  <span className="text-xs font-mono text-muted">Day-1 Upside</span>
                  <span
                    className="text-xl font-bold font-mono"
                    style={{
                      background: SILVER.gradient,
                      WebkitBackgroundClip: "text",
                      WebkitTextFillColor: "transparent",
                    }}
                  >
                    <Redacted />
                  </span>
                  <span className="text-xs text-muted">Pre-sale vs launch price</span>
                </div>
              </div>
            </div>
          </DashedPanel>
        </div>
      </AnimateIn>

      {/* $ANCHAT holder bonus alert */}
      <AnimateIn>
        <div
          className="flex items-start gap-3 px-4 py-4 rounded-lg mt-8"
          style={{ background: "rgba(161,161,170,0.08)", border: `1px solid ${SILVER.border}` }}
        >
          <Coins className="w-5 h-5 shrink-0 mt-0.5" style={{ color: SILVER.light }} />
          <div>
            <span className="text-sm font-display font-bold text-fg">
              $ANCHAT Holder Bonus
            </span>
            <p className="text-xs text-muted leading-relaxed mt-1">
              Hold $ANCHAT tokens from anchat.io? You're eligible to claim a portion of your holdings
              as $ORAMA tokens at no cost. Conversion rate coming soon.
              Connect your Solana wallet at the{" "}
              <Link to="/invest" className="text-accent hover:text-fg transition-colors underline underline-offset-2">
                Investor Dashboard
              </Link>{" "}
              to check your balance and claim.
            </p>
          </div>
        </div>
      </AnimateIn>

      {/* Vesting table */}
      <AnimateIn>
        <div className="mt-8">
          <DashedPanel withBackground>
            <div className="p-4">
              <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-4">
                Vesting Schedule
              </h3>
              <div className="overflow-x-auto">
                <table className="w-full text-left">
                  <thead>
                    <tr className="border-b border-dashed border-border">
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">
                        Allocation
                      </th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">
                        %
                      </th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">
                        Tokens
                      </th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">
                        Cliff
                      </th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">
                        Vesting
                      </th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3">
                        Unlock
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {vestingSchedule.map((row) => (
                      <tr
                        key={row.allocation}
                        className="border-b border-border/50"
                      >
                        <td className="text-sm text-fg py-3 pr-4">
                          {row.allocation}
                        </td>
                        <td className="text-sm text-fg py-3 pr-4 font-mono">
                          {row.pct}
                        </td>
                        <td className="text-sm text-fg py-3 pr-4 font-mono">
                          {row.amount}
                        </td>
                        <td className="text-sm text-muted py-3 pr-4">
                          {row.cliff}
                        </td>
                        <td className="text-sm text-muted py-3 pr-4">
                          {row.schedule}
                        </td>
                        <td className="text-sm text-muted py-3">
                          {row.unlock}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </DashedPanel>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   7. EARNINGS & ROI
   ═══════════════════════════════════════════ */
function EarningsAndRoi() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Earnings & ROI"
          subtitle="Node operators earn $ORAMA rewards for every request served."
        />
      </AnimateIn>

      {/* How earnings work */}
      <AnimateIn>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-8">
          {[
            {
              icon: Server,
              title: "Run a Node",
              desc: "Purchase a license, set up your Orama One hardware or VPS, and join the network. Your node serves compute, storage, and bandwidth to developers.",
            },
            {
              icon: Coins,
              title: "Earn $ORAMA",
              desc: "Every request your node serves earns $ORAMA tokens. Your node also runs the Orama Proxy privacy relay, providing onion-routed traffic for the network.",
            },
            {
              icon: TrendingUp,
              title: "Stake for Multipliers",
              desc: "Stake $ORAMA to boost your reward multiplier from 1x up to 5x. Higher stake signals commitment and earns proportionally more.",
            },
          ].map((item) => (
            <DashedPanel key={item.title} withBackground className="h-full">
              <div className="flex flex-col gap-3 p-3">
                <item.icon className="w-5 h-5 text-accent" />
                <h3 className="font-display font-bold text-fg">
                  {item.title}
                </h3>
                <p className="text-sm text-muted leading-relaxed">
                  {item.desc}
                </p>
              </div>
            </DashedPanel>
          ))}
        </div>
      </AnimateIn>

      {/* Staking tiers */}
      <AnimateIn>
        <div className="mt-8">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-4">
            Staking Tiers
          </h3>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            {[
              {
                tier: "Base",
                stake: "1,000",
                multiplier: "1x",
                color: "#888",
                desc: "Standard rewards for running a node with minimum stake.",
              },
              {
                tier: "Enhanced",
                stake: "10,000",
                multiplier: "2.5x",
                color: "#4169E1",
                desc: "Higher stake unlocks enhanced reward multiplier and priority in job allocation.",
              },
              {
                tier: "Governor",
                stake: "50,000",
                multiplier: "5x",
                color: "#a855f7",
                desc: "Top-tier rewards plus governance voting power. Shape the future of the network.",
              },
            ].map((t) => (
              <DashedPanel key={t.tier} withBackground className="h-full">
                <div
                  className="flex flex-col gap-4 p-4"
                  style={{ borderLeft: `2px solid ${t.color}` }}
                >
                  <div className="flex items-center justify-between">
                    <span className="font-display font-bold text-fg">
                      {t.tier}
                    </span>
                    <Badge variant="outline">{t.tier}</Badge>
                  </div>
                  <div className="flex items-baseline gap-2">
                    <span
                      className="font-mono text-3xl font-bold"
                      style={{ color: t.color }}
                    >
                      {t.multiplier}
                    </span>
                    <span className="text-xs text-muted font-mono uppercase tracking-wider">
                      Multiplier
                    </span>
                  </div>
                  <div className="flex items-baseline gap-1">
                    <span className="font-mono text-lg font-bold text-fg">
                      {t.stake}
                    </span>
                    <span className="text-xs text-muted font-mono">
                      $ORAMA staked
                    </span>
                  </div>
                  <p className="text-sm text-muted leading-relaxed">
                    {t.desc}
                  </p>
                </div>
              </DashedPanel>
            ))}
          </div>
        </div>
      </AnimateIn>

      {/* Reward factors */}
      <AnimateIn>
        <div className="mt-8">
          <SpecTable
            rows={[
              { label: "Reward Distribution", value: "Daily" },
              { label: "Based On", value: "Uptime + bandwidth + compute served" },
              { label: "$ORAMA Source", value: "Details coming soon" },
              { label: "Privacy Layer", value: "Orama Proxy onion routing" },
              { label: "Payment Currency", value: "BTC, $ORAMA" },
            ]}
          />
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   8. ROADMAP — Detailed timeline
   ═══════════════════════════════════════════ */
function Roadmap() {
  const phases = [
    {
      phase: "Phase 1",
      period: "NOW",
      title: "Foundation",
      status: "active" as const,
      items: [
        "50+ nodes running across devnet and testnet",
        "Node license pre-sale open",
        "$ORAMA token pre-sale — details coming soon",
        "Core services live: containers, DNS, databases, WASM",
        "Orama Proxy privacy relay integrated",
      ],
    },
    {
      phase: "Phase 2",
      period: "2025–2026",
      title: "Growth",
      status: "pending" as const,
      items: [
        "Token Generation Event (TGE) — LP launch details coming soon",
        "DEX listings on Raydium",
        "Node operator onboarding at scale",
        "Developer waitlist opens for app deployment",
        "Governance DAO launch",
        "Orama One hardware node pre-orders",
      ],
    },
    {
      phase: "Phase 3",
      period: "2027",
      title: "Expansion",
      status: "pending" as const,
      items: [
        "Orama One hardware nodes shipping",
        "CEX listing applications",
        "Cross-chain bridges (ETH, SOL, ORAMA L1)",
        "Enterprise partnership program",
        "SDK and API v2 with AI compute support",
      ],
    },
    {
      phase: "Phase 4",
      period: "2028",
      title: "Mainnet",
      status: "pending" as const,
      items: [
        "Orama L1 blockchain mainnet launch",
        "Full decentralization — community-owned infrastructure",
        "Proof of Infrastructure consensus live",
        "Pre-sale token holders can trade from day 1",
        "Node license holders migrate to mainnet validators",
      ],
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Roadmap"
          subtitle="From pre-sale to full decentralization."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mt-8">
        {phases.map((phase) => (
          <AnimateIn key={phase.phase}>
            <DashedPanel
              withCorners={phase.status === "active"}
              withBackground
              className={`h-full ${phase.status === "active" ? "border-accent/30" : ""}`}
            >
              <div className="flex flex-col gap-4 p-4">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <StatusDot
                      status={phase.status === "active" ? "active" : "neutral"}
                    />
                    <span className="text-xs font-mono text-accent tracking-wider">
                      {phase.period}
                    </span>
                  </div>
                  <Badge
                    variant={phase.status === "active" ? "default" : "outline"}
                  >
                    {phase.phase}
                  </Badge>
                </div>
                <h3 className="font-display font-bold text-lg text-fg">
                  {phase.title}
                </h3>
                <ul className="flex flex-col gap-2">
                  {phase.items.map((item) => (
                    <li
                      key={item}
                      className="flex items-start gap-2 text-sm text-muted"
                    >
                      <span className="w-1 h-1 rounded-full bg-accent/50 mt-2 shrink-0" />
                      {item}
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
   9. TEAM & PARTNERS
   ═══════════════════════════════════════════ */
function TeamAndPartners() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Team & Partners"
          subtitle="Built by DeBros — a team of builders shipping decentralized infrastructure."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8">
        {/* Team */}
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-3">
                <Code className="w-5 h-5 text-accent" />
                <h3 className="font-display font-bold text-fg">DeBros Team</h3>
              </div>
              <p className="text-sm text-muted leading-relaxed">
                We're not a whitepaper team. Orama Network has working
                infrastructure — 50+ nodes across devnet and testnet, a production CLI,
                container deployments, distributed databases, DNS, WASM functions,
                and a WireGuard mesh overlay. All built and running today.
              </p>
              <div className="flex flex-col gap-2 pt-2">
                {[
                  "Open source — all code public on GitHub",
                  "Active development — shipping weekly",
                  "DeBros NFT community — holders get free node licenses",
                ].map((point) => (
                  <div key={point} className="flex items-start gap-2">
                    <span className="w-1 h-1 rounded-full bg-accent/50 mt-2 shrink-0" />
                    <span className="text-xs text-muted">{point}</span>
                  </div>
                ))}
              </div>
              <Button asChild variant="ghost" size="sm" className="w-fit mt-2">
                <a
                  href="https://github.com/DeBrosOfficial"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  View GitHub
                  <ExternalLink className="w-3 h-3 ml-2" />
                </a>
              </Button>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* ICXCNIKA */}
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-3">
                <img src="/images/icxcnika.webp" alt="ICXCNIKA" className="w-5 h-5 object-contain" />
                <h3 className="font-display font-bold text-fg">ICXCNIKA</h3>
                <Badge variant="outline">Partner</Badge>
              </div>
              <p className="text-sm text-muted leading-relaxed">
                ICXCNIKA is an early partner and supporter of the Orama Network,
                helping drive adoption and growth of decentralized infrastructure
                across the ecosystem.
              </p>
              <Button asChild variant="ghost" size="sm" className="w-fit mt-2">
                <a href="https://icxcnika.io/" target="_blank" rel="noopener noreferrer">
                  icxcnika.io
                  <ExternalLink className="w-3 h-3 ml-2" />
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
   10. FAQ
   ═══════════════════════════════════════════ */
function Faq() {
  const faqs = [
    {
      question: "What's the difference between a node license and the token pre-sale?",
      answer: "A node license gives you the right to operate an Orama Network node and earn $ORAMA rewards for serving compute. The token pre-sale lets you buy $ORAMA tokens at a discount before the public launch. Node licenses are for active participants who want to run infrastructure. The pre-sale is for those who want token exposure without operating a node. Pricing details coming soon.",
    },
    {
      question: "What hardware do I need to run a node?",
      answer: "You can run a node on any VPS with 4+ CPU cores, 8GB+ RAM, and 100GB+ SSD storage. Alternatively, we're developing Orama One — a pre-built hardware node that you plug in and forget. Orama One pre-orders open during Phase 2 (2025-2026).",
    },
    {
      question: "When can I trade my pre-sale tokens?",
      answer: "Pre-sale tokens have a vesting period with a cliff. After the cliff, tokens unlock monthly over the remaining period. Once vested, tokens are fully tradeable on Orama DEX and any future CEX listings. Exact vesting schedule coming soon.",
    },
    {
      question: "What is the launch price based on?",
      answer: "A portion of the raise is allocated to seed the initial liquidity pool on Raydium. The launch price includes a premium over the pre-sale price, giving early investors built-in upside at launch. Exact pricing details coming soon.",
    },
    {
      question: "How do DeBros NFT holders claim their free node license?",
      answer: "If you hold a DeBros Team NFT, visit debros.io/nft and connect your wallet. Your free node license will be issued automatically. One license per NFT. The license is identical to purchased licenses — same rights, same rewards.",
    },
    {
      question: "How do node rewards work?",
      answer: "Every Orama node runs Orama Network services (compute, storage, DNS) and the Orama Proxy privacy relay. You earn $ORAMA rewards from a single node with no extra configuration.",
    },
    {
      question: "Is there a token vesting schedule for the team?",
      answer: "Yes. The core team allocation has a multi-year vesting schedule with a cliff period. This means zero team tokens are liquid initially, and then they unlock monthly over the remaining period. This aligns team incentives with long-term network success. Exact schedule coming soon.",
    },
    {
      question: "What happens if the raise falls short of the target?",
      answer: "DeBros will personally fund the LP if the raise falls short, ensuring the planned launch price and strong market depth regardless of pre-sale performance. We are committed to the success of Orama Network and will ensure a successful launch even if we don't hit the full raise target. That funding comes from revenue from DeBros Applications like AnChat.",
    },
    {
      question: "Can I resell my node license?",
      answer: "No — node licenses are non-transferable and tied to the purchaser's wallet. This ensures that all node operators are known entities, which is important for network security and integrity. If you want to exit your position, you can sell your $ORAMA tokens on the open market after they vest.",
    },
  ];

  const [openIndex, setOpenIndex] = useState<number | null>(null);

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="FAQ"
          subtitle="Everything you need to know before investing."
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
   11. FINAL CTA + CONTACT
   ═══════════════════════════════════════════ */
function FinalCta() {
  return (
    <Section padding="wide">
      {/* Big CTA */}
      <AnimateIn>
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col items-center text-center gap-6 py-8 px-4">
            <StatusDot status="active" />
            <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
              Ready to back the decentralized cloud?
            </h2>
            <p className="text-muted max-w-lg text-sm leading-relaxed">
              Node licenses and token pre-sale details coming soon. Supply is limited.
            </p>
            <div className="flex flex-wrap items-center gap-3 justify-center">
              <span className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm text-black opacity-50 pointer-events-none">
                Coming Soon
              </span>
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
   PAGE ASSEMBLY
   ═══════════════════════════════════════════ */
export default function Investors() {
  return (
    <Page title="Invest in Orama Network — Node Licenses & Token Pre-Sale">
      <InvestorHero />
      <Suspense fallback={null}>
        <GrowthVaultScene />
      </Suspense>
      <Section padding="none"><CrosshairDivider /></Section>
      <TheOpportunity />
      <Section padding="none"><CrosshairDivider /></Section>
      <TwoWaysToInvest />
      <Section padding="none"><CrosshairDivider /></Section>
      <DebrosNftCallout />
      <Section padding="none"><CrosshairDivider /></Section>
      <AnchatHolderCallout />
      <Section padding="none"><CrosshairDivider /></Section>
      <FundAllocation />
      <Section padding="none"><CrosshairDivider /></Section>
      <TokenomicsDeepDive />
      <Section padding="none"><CrosshairDivider /></Section>
      <EarningsAndRoi />
      <Section padding="none"><CrosshairDivider /></Section>
      <Roadmap />
      <Section padding="none"><CrosshairDivider /></Section>
      <TeamAndPartners />
      <Section padding="none"><CrosshairDivider /></Section>
      <SponsorsShowcase />
      <Section padding="none"><CrosshairDivider /></Section>
      <Faq />
      <Section padding="none"><CrosshairDivider /></Section>
      <FinalCta />
    </Page>
  );
}
