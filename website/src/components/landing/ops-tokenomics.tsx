import { Link } from "react-router";
import { Coins, Vote, CreditCard, ArrowRight, AlertTriangle } from "lucide-react";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { MetricCard } from "../ui/metric-card";
import { DashedPanel } from "../ui/dashed-panel";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";
import { Button } from "../ui/button";
import { SILVER } from "../ui/silver-theme";

const PER_NODE_EARNINGS = [
  { nodes: "300", daily: "3,840", monthly: "115,200" },
  { nodes: "500", daily: "2,304", monthly: "69,120" },
  { nodes: "1,000", daily: "1,152", monthly: "34,560" },
  { nodes: "5,000", daily: "230", monthly: "6,912" },
];

export function OpsTokenomics() {
  return (
    <>
      <Section id="tokenomics">
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Reward Structure"
              subtitle="Earn $ORAMA through hybrid consensus. Your Effective Power determines your share of block rewards."
            />

            {/* Key metrics */}
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 max-w-4xl mx-auto w-full">
              <DashedPanel className="p-4">
                <MetricCard label="Block Reward (Era 1)" value="100 $ORAMA" />
              </DashedPanel>
              <DashedPanel className="p-4">
                <MetricCard label="Block Time" value="6 seconds" />
              </DashedPanel>
              <DashedPanel className="p-4">
                <MetricCard label="To Miners" value="80%" />
              </DashedPanel>
              <DashedPanel className="p-4">
                <MetricCard label="Halving" value="Every 2 Years" />
              </DashedPanel>
            </div>

            {/* Effective Power formula */}
            <DashedPanel withCorners withBackground>
              <div className="flex flex-col gap-4 p-6">
                <h3 className="font-display font-bold text-fg">Effective Power Formula</h3>
                <div className="bg-bg/50 border border-dashed border-border rounded-sm p-4 font-mono text-sm text-fg text-center">
                  Effective Power = Staked $ORAMA &times; (1 + Contribution Score) &times; Infrastructure Multiplier
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mt-2">
                  <div className="flex flex-col gap-1">
                    <span className="text-xs font-mono text-muted uppercase tracking-wider">Contribution Score</span>
                    <p className="text-xs text-muted leading-relaxed">
                      40% uptime, 30% bandwidth, 20% compute, 10% reliability. Measured every epoch (1 hour).
                    </p>
                  </div>
                  <div className="flex flex-col gap-1">
                    <span className="text-xs font-mono text-muted uppercase tracking-wider">Infrastructure Multiplier</span>
                    <p className="text-xs text-muted leading-relaxed">
                      OramaOS = 1.5x. Standard OS = 1.0x. TPM-based attestation verified on-chain.
                    </p>
                  </div>
                  <div className="flex flex-col gap-1">
                    <span className="text-xs font-mono text-muted uppercase tracking-wider">Testnet Bootstrap</span>
                    <p className="text-xs text-muted leading-relaxed">
                      No staking required on testnet. Tokens earned carry over to mainnet. 1,000 $ORAMA min stake at mainnet.
                    </p>
                  </div>
                </div>
              </div>
            </DashedPanel>

            {/* Per-node earnings table */}
            <DashedPanel withCorners withBackground>
              <div className="flex flex-col gap-4 p-6">
                <h3 className="font-display font-bold text-fg">Per-Node Earnings (Era 1)</h3>
                <p className="text-xs text-muted">Assumes equal Effective Power across all nodes.</p>
                <div className="overflow-x-auto">
                  <table className="w-full text-xs sm:text-sm min-w-[400px]">
                    <thead>
                      <tr className="border-b" style={{ borderColor: SILVER.border }}>
                        <th className="text-left p-2 sm:p-3 font-mono text-[10px] sm:text-xs text-zinc-500 uppercase tracking-wider">Total Nodes</th>
                        <th className="text-left p-2 sm:p-3 font-mono text-[10px] sm:text-xs text-zinc-500 uppercase tracking-wider">Daily per Node</th>
                        <th className="text-left p-2 sm:p-3 font-mono text-[10px] sm:text-xs text-zinc-500 uppercase tracking-wider">Monthly per Node</th>
                      </tr>
                    </thead>
                    <tbody>
                      {PER_NODE_EARNINGS.map((row, i) => (
                        <tr
                          key={row.nodes}
                          className={i < PER_NODE_EARNINGS.length - 1 ? "border-b" : ""}
                          style={{ borderColor: SILVER.border }}
                        >
                          <td className="p-2 sm:p-3 font-display font-bold text-fg text-xs">{row.nodes}</td>
                          <td className="p-2 sm:p-3 text-xs" style={{ color: SILVER.light }}>{row.daily} $ORAMA</td>
                          <td className="p-2 sm:p-3 text-xs" style={{ color: SILVER.light }}>{row.monthly} $ORAMA</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            </DashedPanel>

            {/* Slashing rules */}
            <DashedPanel withCorners>
              <div className="flex flex-col gap-4 p-6">
                <div className="flex items-center gap-2">
                  <AlertTriangle className="w-4 h-4 text-amber-500" />
                  <h3 className="font-display font-bold text-fg">Slashing Rules</h3>
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
                  <div className="flex flex-col gap-1">
                    <span className="text-xs font-mono font-bold text-red-400">100% SLASH</span>
                    <p className="text-xs text-muted">Double-signing or cheating. Tokens burned.</p>
                  </div>
                  <div className="flex flex-col gap-1">
                    <span className="text-xs font-mono font-bold text-amber-400">5-30% SLASH</span>
                    <p className="text-xs text-muted">Downtime exceeding 20%. Progressive penalty.</p>
                  </div>
                  <div className="flex flex-col gap-1">
                    <span className="text-xs font-mono font-bold text-orange-400">50% SLASH</span>
                    <p className="text-xs text-muted">False infrastructure attestation. Permanently flagged.</p>
                  </div>
                </div>
                <p className="text-[10px] font-mono text-muted">All slashed tokens are burned permanently — not redistributed.</p>
              </div>
            </DashedPanel>

            {/* Utility summary + CTA */}
            <div className="flex flex-col sm:flex-row items-center justify-between gap-4 border border-dashed border-border p-5">
              <div className="flex flex-wrap gap-6 text-sm text-muted">
                <div className="flex items-center gap-2">
                  <CreditCard className="w-4 h-4 text-accent" />
                  <span>BTC-only economy</span>
                </div>
                <div className="flex items-center gap-2">
                  <Vote className="w-4 h-4 text-accent" />
                  <span>On-chain governance</span>
                </div>
                <div className="flex items-center gap-2">
                  <Coins className="w-4 h-4 text-accent" />
                  <span>210M hard cap — zero pre-mine</span>
                </div>
              </div>
              <Button asChild variant="ghost" size="sm">
                <Link to="/token" className="flex items-center gap-1.5">
                  Full Tokenomics
                  <ArrowRight className="w-3.5 h-3.5" />
                </Link>
              </Button>
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>
    </>
  );
}
