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
  ExternalLink,
  TrendingUp,
  Globe,
  Zap,
  ChevronRight,
  Award,
  Code,
  Flame,
  Lock,
} from "lucide-react";

/* ═══════════════════════════════════════════
   1. HERO — The base layer for 1,000 years
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
            text="The base layer for"
            className="text-fg"
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
          />
          <br />
          <SplitText
            text="the next 1,000 years."
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
          210,000,000 $ORAMA. Hard cap forever. Zero pre-mine. Zero team allocation.
          Zero airdrop. 100% earned through mining — just like Bitcoin.
        </p>

        {/* Key stats */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 w-full max-w-lg">
          {[
            { label: "Hard Cap", value: "210M" },
            { label: "Mined", value: "100%" },
            { label: "Pre-mine", value: "0" },
            { label: "Genesis Nodes", value: "300" },
          ].map((s) => (
            <div key={s.label} className="flex flex-col items-center gap-1">
              <span
                className="text-2xl font-bold font-mono tabular-nums"
                style={{
                  background: SILVER.gradient,
                  WebkitBackgroundClip: "text",
                  WebkitTextFillColor: "transparent",
                }}
              >
                {s.value}
              </span>
              <span className="text-[10px] font-mono text-muted tracking-wider uppercase">
                {s.label}
              </span>
            </div>
          ))}
        </div>

        {/* CTA */}
        <div className="flex flex-wrap items-center gap-3 justify-center pt-2">
          <a href="#participate" className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm cursor-pointer text-black">
            How to Participate
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
          subtitle="Cloud + money in one protocol. BTC-native. Real infrastructure live today."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-8">
        {[
          {
            icon: Globe,
            title: "Cloud + money in one protocol",
            desc: "Orama is both a decentralized world computer (SQL, KV, IPFS, WASM, AI) and a Bitcoin-grade financial system. No other chain does both.",
          },
          {
            icon: Zap,
            title: "BTC-native economy",
            desc: "Only two assets exist: BTC and $ORAMA. No stablecoins, no wrapped altcoins. Bridge BTC, buy $ORAMA, done. Zero counterparty risk beyond Bitcoin itself.",
          },
          {
            icon: TrendingUp,
            title: "Real infrastructure, live today",
            desc: "Orama Network isn't a whitepaper project. Nodes are running across multiple environments with a working CLI, container deployments, DNS, databases, and WASM functions — all live.",
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
    </Section>
  );
}

/* ═══════════════════════════════════════════
   3. HOW $ORAMA IS CREATED — Mining model
   ═══════════════════════════════════════════ */
function HowOramaIsCreated() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="How $ORAMA Is Created"
          subtitle="Bitcoin-style mining with halving every 2 years. 50% emitted in the first 2 years."
        />
      </AnimateIn>

      {/* Explanation */}
      <AnimateIn>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-8">
          {[
            {
              icon: Server,
              title: "Block Rewards",
              desc: "Every 6 seconds, a new block is produced. The block proposer receives 80% of the reward. 20% flows to the protocol bonding curve (capped at 21M tokens).",
            },
            {
              icon: TrendingUp,
              title: "Bitcoin-Style Halving",
              desc: "Era 1 starts at 100 $ORAMA per block. Every 2 years, the reward halves. Predictable, verifiable at any block height. 50% emitted in year 1-2.",
            },
            {
              icon: Flame,
              title: "Deflationary Fees",
              desc: "All base fees are burned permanently. Priority fees go to block proposers. The more the network is used, the more $ORAMA is removed from supply forever.",
            },
          ].map((item) => (
            <DashedPanel key={item.title} withBackground className="h-full">
              <div className="flex flex-col gap-3 p-3">
                <item.icon className="w-5 h-5 text-accent" />
                <h3 className="font-display font-bold text-fg">{item.title}</h3>
                <p className="text-sm text-muted leading-relaxed">{item.desc}</p>
              </div>
            </DashedPanel>
          ))}
        </div>
      </AnimateIn>

      {/* Emission table */}
      <AnimateIn>
        <div className="mt-8">
          <DashedPanel withBackground>
            <div className="p-4">
              <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-4">
                Emission Schedule
              </h3>
              <div className="overflow-x-auto">
                <table className="w-full text-left">
                  <thead>
                    <tr className="border-b border-dashed border-border">
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Era</th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Years</th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Block Reward</th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">To Miners (80%)</th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">To Curve (20%)</th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3">Cumulative</th>
                    </tr>
                  </thead>
                  <tbody>
                    {[
                      { era: "1", years: "1-2", reward: "100", miners: "84.1M", curve: "21M", cumulative: "105.1M (50%)" },
                      { era: "2", years: "3-4", reward: "50", miners: "42M", curve: "10.5M", cumulative: "157.7M (75%)" },
                      { era: "3", years: "5-6", reward: "25", miners: "21M", curve: "5.3M", cumulative: "184M (87.5%)" },
                      { era: "4", years: "7-8", reward: "12.5", miners: "10.5M", curve: "2.6M", cumulative: "197.1M (93.8%)" },
                      { era: "5", years: "9-10", reward: "6.25", miners: "5.3M", curve: "1.3M", cumulative: "203.7M (96.9%)" },
                    ].map((row) => (
                      <tr key={row.era} className="border-b border-border/50">
                        <td className="text-sm text-fg py-3 pr-4 font-mono font-bold">{row.era}</td>
                        <td className="text-sm text-muted py-3 pr-4">{row.years}</td>
                        <td className="text-sm text-fg py-3 pr-4 font-mono">{row.reward} $ORAMA</td>
                        <td className="text-sm text-muted py-3 pr-4 font-mono">{row.miners}</td>
                        <td className="text-sm text-muted py-3 pr-4 font-mono">{row.curve}</td>
                        <td className="text-sm text-fg py-3 font-mono">{row.cumulative}</td>
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
   4. THREE WAYS TO PARTICIPATE
   ═══════════════════════════════════════════ */
function ThreeWaysToParticipate() {
  return (
    <Section id="participate">
      <AnimateIn>
        <SectionHeader
          title="Three Ways to Participate"
          subtitle="Run a node. Buy from the bonding curve. Or acquire a node license."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 mt-8">
        {/* Card A: Run a Node */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-5 p-4">
              <div className="flex items-center gap-3">
                <Server className="w-6 h-6 text-accent" />
                <div>
                  <h3 className="font-display font-bold text-lg text-fg">Run a Node</h3>
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Mine $ORAMA</span>
                </div>
              </div>

              <p className="text-sm text-muted leading-relaxed">
                Mine $ORAMA by running a node. Testnet is free — no staking required. Tokens earned during
                testnet are real and carry over to mainnet. The earlier you join, the more you earn.
              </p>

              <div className="flex flex-col gap-2">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">
                  Per-Node Earnings (Era 1)
                </span>
                <div className="flex flex-col gap-1">
                  {[
                    { nodes: "300 nodes", daily: "3,840 $ORAMA/day" },
                    { nodes: "500 nodes", daily: "2,304 $ORAMA/day" },
                    { nodes: "1,000 nodes", daily: "1,152 $ORAMA/day" },
                  ].map((r) => (
                    <div key={r.nodes} className="flex items-center justify-between text-xs font-mono py-1 border-b border-border/30">
                      <span className="text-muted">{r.nodes}</span>
                      <span className="text-fg">{r.daily}</span>
                    </div>
                  ))}
                </div>
                <span className="text-[10px] text-muted">Assumes equal Effective Power. Actual earnings vary.</span>
              </div>

              <div className="flex items-center gap-2">
                <StatusDot status="active" />
                <span className="text-xs font-mono text-accent">Testnet Live Now</span>
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Card B: Bonding Curve */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-5 p-4">
              <div className="flex items-center gap-3">
                <Coins className="w-6 h-6 text-accent" />
                <div>
                  <h3 className="font-display font-bold text-lg text-fg">Bonding Curve</h3>
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Coming Soon</span>
                </div>
              </div>

              <p className="text-sm text-muted leading-relaxed">
                The protocol itself is the first market maker. 20% of every block reward flows into
                the curve's inventory. Price follows a square root function — cheap early, expensive later.
                BTC paid goes directly to the protocol reserve, backing the BTC bridge.
              </p>

              <div className="flex flex-col gap-2">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">
                  Price Schedule (Price = k x sqrt(n))
                </span>
                <div className="flex flex-col gap-1">
                  {[
                    { sold: "10K sold", price: "0.00000006 BTC" },
                    { sold: "1M sold", price: "0.0000006 BTC" },
                    { sold: "10M sold", price: "0.0000019 BTC" },
                    { sold: "21M (max)", price: "0.00000275 BTC" },
                  ].map((r) => (
                    <div key={r.sold} className="flex items-center justify-between text-xs font-mono py-1 border-b border-border/30">
                      <span className="text-muted">{r.sold}</span>
                      <span className="text-fg">{r.price}</span>
                    </div>
                  ))}
                </div>
                <span className="text-[10px] text-muted">Total BTC to fill curve: ~38.5 BTC. Max 21M tokens.</span>
              </div>

              <button disabled className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm text-black w-full opacity-50 cursor-not-allowed">
                Coming Soon
              </button>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Card C: Node License */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-5 p-4">
              <div className="flex items-center gap-3">
                <Lock className="w-6 h-6 text-accent" />
                <div>
                  <h3 className="font-display font-bold text-lg text-fg">Node License</h3>
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">Coming Soon</span>
                </div>
              </div>

              <p className="text-sm text-muted leading-relaxed">
                Node licenses will provide the right to operate an Orama Network node with priority access
                and additional benefits. Details are being finalized and will be announced soon.
              </p>

              <div className="flex-1 flex items-center justify-center py-8">
                <span className="text-sm font-mono text-muted">Details TBA</span>
              </div>

              <button disabled className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm text-black w-full opacity-50 cursor-not-allowed">
                Coming Soon
              </button>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   5. BTC BRIDGE REVENUE ENGINE
   ═══════════════════════════════════════════ */
function BtcBridgeRevenue() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="BTC Bridge Revenue Engine"
          subtitle="0.25% fee on every bridge transaction. A perpetual revenue flywheel."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8">
        {/* Fee split */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-5 p-4">
              <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
                Bridge Fee: 0.25%
              </h3>

              <div className="flex flex-col gap-3">
                <div className="flex items-center justify-between py-3 border-b border-dashed border-border">
                  <span className="text-sm text-fg font-semibold">50% to Validators</span>
                  <span className="text-xs font-mono text-muted">Paid directly in BTC, by Effective Power</span>
                </div>
                <div className="flex items-center justify-between py-3 border-b border-dashed border-border">
                  <span className="text-sm text-fg font-semibold">50% to Team NFT Holders</span>
                  <span className="text-xs font-mono text-muted">Auto-swapped to $ORAMA on order book</span>
                </div>
              </div>

              <div className="flex flex-col gap-1">
                <span className="text-xs font-mono text-muted">Min bridge: 0.001 BTC. No maximum.</span>
                <span className="text-xs font-mono text-muted">Security: Bitcoin light-client + zk-proofs + BitVM fraud proofs</span>
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Flywheel */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-5 p-4">
              <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
                Revenue Flywheel
              </h3>

              <div className="flex flex-col gap-3">
                {[
                  "More bridge usage",
                  "More BTC fees collected",
                  "More $ORAMA auto-bought on order book (buy pressure)",
                  "NFT holders receive more $ORAMA",
                  "NFTs become more valuable",
                  "More attention on Orama",
                  "More users and bridge usage",
                ].map((step, i) => (
                  <div key={i} className="flex items-start gap-3">
                    <span
                      className="font-mono text-xs shrink-0 w-5 h-5 flex items-center justify-center border border-dashed rounded-sm mt-0.5"
                      style={{ borderColor: SILVER.dark, color: SILVER.light }}
                    >
                      {i + 1}
                    </span>
                    <span className="text-sm text-muted">{step}</span>
                  </div>
                ))}
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   6. DEBROS NFTs — Governance + Revenue
   ═══════════════════════════════════════════ */
function DebrosNfts() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="DeBros NFTs"
          subtitle="NFT holders control 75% of governance. No whale capture — ever."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mt-8">
        {/* Team NFTs */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-3">
                <Award className="w-6 h-6 text-accent" />
                <div>
                  <h3 className="font-display font-bold text-lg text-fg">DeBros Team NFTs</h3>
                  <Badge variant="outline">100 Supply</Badge>
                </div>
              </div>
              <div className="flex flex-col gap-2">
                {[
                  "40% of total governance voting power (5 votes per NFT)",
                  "50% of BTC bridge fees auto-swapped to $ORAMA every epoch",
                  "Freely tradeable on Orama marketplace",
                  "Migrated from Solana at mainnet via snapshot",
                ].map((point) => (
                  <div key={point} className="flex items-start gap-2">
                    <span className="w-1 h-1 rounded-full bg-accent/50 mt-2 shrink-0" />
                    <span className="text-sm text-muted">{point}</span>
                  </div>
                ))}
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>

        {/* Community NFTs */}
        <AnimateIn>
          <DashedPanel withCorners withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-3">
                <Award className="w-6 h-6 text-accent" />
                <div>
                  <h3 className="font-display font-bold text-lg text-fg">DeBros Community NFTs</h3>
                  <Badge variant="outline">700 Supply</Badge>
                </div>
              </div>
              <div className="flex flex-col gap-2">
                {[
                  "35% of total governance voting power (1 vote per NFT)",
                  "Freely tradeable on Orama marketplace",
                  "Migrated from Solana at mainnet via snapshot",
                  "Together with Team NFTs: 75% of all governance",
                ].map((point) => (
                  <div key={point} className="flex items-start gap-2">
                    <span className="w-1 h-1 rounded-full bg-accent/50 mt-2 shrink-0" />
                    <span className="text-sm text-muted">{point}</span>
                  </div>
                ))}
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>
      </div>

      {/* Governance breakdown */}
      <AnimateIn>
        <div className="mt-8">
          <SpecTable
            rows={[
              { label: "Team NFTs (100)", value: "40% governance, 5 votes each, bridge fee revenue" },
              { label: "Community NFTs (700)", value: "35% governance, 1 vote each" },
              { label: "$ORAMA Token Holders", value: "25% governance, quadratic voting (sqrt of tokens held)" },
              { label: "Emergency (Tier 1)", value: "Team NFTs only, 24h, 60% threshold" },
              { label: "Protocol Upgrades (Tier 2)", value: "All vote, 3 days, 66% threshold" },
              { label: "Constitutional (Tier 3)", value: "All vote, 14 days, 90% threshold" },
            ]}
          />
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   7. CONSENSUS & SECURITY
   ═══════════════════════════════════════════ */
function ConsensusAndSecurity() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Consensus & Security"
          subtitle="Hybrid PoS + Proof of Contribution + Proof of Infrastructure. Real nodes, real work."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8">
        <AnimateIn>
          <div className="flex flex-col gap-4">
            <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
              Effective Power Formula
            </h3>
            <DashedPanel withBackground>
              <div className="p-4">
                <code className="text-sm text-fg font-mono">
                  Effective Power = Staked $ORAMA x (1 + Contribution Score) x Infrastructure Multiplier
                </code>
              </div>
            </DashedPanel>

            <div className="flex flex-col gap-3">
              {[
                { label: "Block Time", value: "6 seconds" },
                { label: "Epoch", value: "1 hour (600 blocks)" },
                { label: "Finality", value: "BFT checkpoints every epoch" },
                { label: "Min Stake (Mainnet)", value: "1,000 $ORAMA" },
                { label: "Testnet Staking", value: "Not required" },
                { label: "OramaOS Multiplier", value: "1.5x (without = 1.0x)" },
              ].map((row) => (
                <div key={row.label} className="flex items-center justify-between text-sm py-1 border-b border-border/30">
                  <span className="text-muted">{row.label}</span>
                  <span className="text-fg font-mono">{row.value}</span>
                </div>
              ))}
            </div>
          </div>
        </AnimateIn>

        <AnimateIn>
          <div className="flex flex-col gap-4">
            <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
              Slashing & Contribution
            </h3>
            <div className="flex flex-col gap-3">
              {[
                { icon: Shield, title: "Double-signing", desc: "100% slash. Zero tolerance for equivocation." },
                { icon: Shield, title: "Downtime (20-80%)", desc: "5-30% progressive slash based on severity." },
                { icon: Shield, title: "False attestation", desc: "50% slash for faking OramaOS infrastructure proofs." },
              ].map((item) => (
                <DashedPanel key={item.title} withBackground>
                  <div className="flex gap-3 p-3">
                    <item.icon className="w-4 h-4 mt-0.5 shrink-0 text-accent" />
                    <div>
                      <h4 className="font-display font-bold text-sm text-fg">{item.title}</h4>
                      <p className="text-xs text-muted leading-relaxed mt-1">{item.desc}</p>
                    </div>
                  </div>
                </DashedPanel>
              ))}
            </div>

            <h3 className="text-xs font-mono text-muted tracking-wider uppercase mt-2">
              Contribution Score (per epoch)
            </h3>
            <div className="flex flex-col gap-1">
              {[
                { factor: "Uptime", weight: "40%" },
                { factor: "Bandwidth served", weight: "30%" },
                { factor: "Compute/storage/SQL", weight: "20%" },
                { factor: "Latency & reliability", weight: "10%" },
              ].map((row) => (
                <div key={row.factor} className="flex items-center justify-between text-xs font-mono py-1 border-b border-border/30">
                  <span className="text-muted">{row.factor}</span>
                  <span className="text-fg">{row.weight}</span>
                </div>
              ))}
            </div>
          </div>
        </AnimateIn>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   8. ROADMAP — Milestones not dates
   ═══════════════════════════════════════════ */
function Roadmap() {
  const phases = [
    {
      phase: "Testnet",
      period: "NOW",
      title: "Testnet",
      status: "active" as const,
      items: [
        "Network live, no staking required",
        "Node runners begin earning $ORAMA block rewards",
        "PLONK trusted setup ceremony",
        "Bug bounty program",
        "Core services live: containers, DNS, databases, WASM",
      ],
    },
    {
      phase: "Expansion",
      period: "NEXT",
      title: "Testnet Expansion",
      status: "pending" as const,
      items: [
        "AI Marketplace beta with Angels framework",
        "Compute provider registration",
        "Orama One hardware node pre-orders",
        "Developer onboarding at scale",
      ],
    },
    {
      phase: "Maturity",
      period: "UPCOMING",
      title: "Testnet Maturity",
      status: "pending" as const,
      items: [
        "300-node threshold target",
        "DeBros NFT migration preparation",
        "Bonding curve live on testnet",
        "Native order book testing",
      ],
    },
    {
      phase: "Mainnet",
      period: "MILESTONE",
      title: "Mainnet",
      status: "pending" as const,
      items: [
        "Full production launch with BTC bridge live",
        "Native DEX live (order book + bonding curve)",
        "Staking activated (1,000 $ORAMA minimum)",
        "DeBros NFT bridge revenue begins",
        "On-chain governance live",
      ],
    },
    {
      phase: "Post-Launch",
      period: "LONG-TERM",
      title: "Post-Launch",
      status: "pending" as const,
      items: [
        "L2 rollup support",
        "AI Marketplace expansion",
        "Post-quantum signature upgrade",
        "Orama One general availability",
        "Bonding curve sunset when organic liquidity is sufficient",
      ],
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Roadmap"
          subtitle="Milestones, not dates. Each phase must be earned."
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
          subtitle="Built by DeBros — shipping decentralized infrastructure."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mt-8">
        <AnimateIn>
          <DashedPanel withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4">
              <div className="flex items-center gap-3">
                <Code className="w-5 h-5 text-accent" />
                <h3 className="font-display font-bold text-fg">DeBros Team</h3>
              </div>
              <p className="text-sm text-muted leading-relaxed">
                Not a whitepaper team. Orama Network has working infrastructure — nodes
                running across multiple environments, a production CLI, container deployments,
                distributed databases, DNS, WASM functions, and a WireGuard mesh overlay.
                All built and running today.
              </p>
              <div className="flex flex-col gap-2 pt-2">
                {[
                  "Open source — all code public on GitHub",
                  "Active development — shipping weekly",
                  "The creators earn $ORAMA the same way as everyone else: by running nodes",
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
   10. FAQ — Aligned with whitepaper
   ═══════════════════════════════════════════ */
function Faq() {
  const faqs = [
    {
      question: "How is $ORAMA created?",
      answer: "100% of $ORAMA is earned through mining — running a node and producing blocks. There is no pre-mine, no team allocation, no airdrop, and no investor round. The creators earn tokens the same way as everyone else: by running nodes.",
    },
    {
      question: "What is the bonding curve?",
      answer: "The protocol itself acts as the first market maker. 20% of every block reward flows into the bonding curve's inventory (capped at 21M tokens). Anyone can buy $ORAMA from the curve by sending BTC. The price follows a square root function: Price = k x sqrt(tokens_sold). The curve starts cheap and rises as demand grows. All BTC paid goes to the protocol reserve, backing the BTC bridge.",
    },
    {
      question: "Why is it BTC-only?",
      answer: "Orama has exactly two assets: BTC and $ORAMA. No stablecoins, no wrapped altcoins, no fiat pegs. This eliminates counterparty risk from stablecoin depegs, altcoin crashes, or centralized token issuers. Hard money priced in hard money.",
    },
    {
      question: "Do I need to stake to run a testnet node?",
      answer: "No. During testnet, no staking is required. Any node operator can participate and earn $ORAMA block rewards with zero stake. Testnet tokens carry over to mainnet — there is no reset. At mainnet launch, the 1,000 $ORAMA minimum stake activates.",
    },
    {
      question: "What hardware do I need?",
      answer: "You can run a node on any VPS with modest specs. Alternatively, Orama One is a purpose-built hardware node that ships pre-loaded with OramaOS — plug in and it joins the network automatically. Hardware specs are published in the whitepaper appendix.",
    },
    {
      question: "What is OramaOS?",
      answer: "OramaOS is a custom hardened operating system for Orama nodes. No remote shell access, read-only root filesystem, full-disk encryption, atomic updates. Running OramaOS provides a 1.5x Infrastructure Multiplier for block rewards. Without OramaOS you still earn, but at 1.0x.",
    },
    {
      question: "How does governance work?",
      answer: "NFT holders (Team + Community) control 75% of governance voting power. Token holders get 25% with quadratic voting. Three tiers: Emergency (24h, Team NFTs only), Protocol upgrades (3 days, 66%), Constitutional changes (14 days, 90%). The immutable financial core (210M cap, emission schedule, BTC-only, 100% mining) cannot be changed by any vote.",
    },
    {
      question: "Is there slashing?",
      answer: "Yes. Double-signing = 100% slash. Downtime above 20% = 5-30% progressive slash. False infrastructure attestation = 50% slash. Slashing is essential for network security in a real proof-of-stake system.",
    },
    {
      question: "How does the native DEX work?",
      answer: "Orama has a protocol-native order book (not an AMM) for the $ORAMA/BTC pair. Any holder can place limit orders, market orders, or cancel orders. The bonding curve acts as a guaranteed liquidity backstop. Custom tokens created via WASM contracts trade against $ORAMA on permissionless WASM DEX contracts.",
    },
  ];

  const [openIndex, setOpenIndex] = useState<number | null>(null);

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="FAQ"
          subtitle="Common questions about $ORAMA and the Orama Network."
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
function FinalCta() {
  return (
    <Section padding="wide">
      <AnimateIn>
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col items-center text-center gap-6 py-8 px-4">
            <StatusDot status="active" />
            <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
              The power belongs to the people.
            </h2>
            <p className="text-muted max-w-lg text-sm leading-relaxed">
              No tokens to buy beforehand. No presale to miss. Just run a node, earn $ORAMA,
              and be part of the only blockchain where everyone starts equal.
            </p>
            <div className="flex flex-wrap items-center gap-3 justify-center">
              <Link to="/invest">
                <span className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm text-black">
                  Get Started
                  <ArrowRight className="w-4 h-4 ml-2" />
                </span>
              </Link>
              <a href="https://t.me/debrosportal" target="_blank" rel="noopener noreferrer">
                <Button variant="dashed" className="font-mono tracking-wider uppercase px-8 py-3 text-sm">
                  JOIN COMMUNITY <ArrowRight className="w-4 h-4 ml-2" />
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
    <Page title="Invest in Orama Network — 210M Hard Cap, 100% Mined, Zero Pre-mine">
      <InvestorHero />
      <Suspense fallback={null}>
        <GrowthVaultScene />
      </Suspense>
      <Section padding="none"><CrosshairDivider /></Section>
      <TheOpportunity />
      <Section padding="none"><CrosshairDivider /></Section>
      <HowOramaIsCreated />
      <Section padding="none"><CrosshairDivider /></Section>
      <ThreeWaysToParticipate />
      <Section padding="none"><CrosshairDivider /></Section>
      <BtcBridgeRevenue />
      <Section padding="none"><CrosshairDivider /></Section>
      <DebrosNfts />
      <Section padding="none"><CrosshairDivider /></Section>
      <ConsensusAndSecurity />
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
