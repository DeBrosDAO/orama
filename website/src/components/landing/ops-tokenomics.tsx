import { Link } from "react-router";
import { Coins, Vote, CreditCard, Server, ArrowRight } from "lucide-react";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { MetricCard } from "../ui/metric-card";
import { DashedPanel } from "../ui/dashed-panel";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";
import { Button } from "../ui/button";
import { Badge } from "../ui/badge";

const TIER_ACCENT: Record<string, string> = {
  Base: "#888",
  Enhanced: "#4169E1",
  Governor: "#a855f7",
};

const rewardTiers = [
  {
    icon: <Server className="w-5 h-5" />,
    tier: "Base",
    stake: "***",
    multiplier: "***",
    description: "Standard rewards for running a node with minimum stake",
  },
  {
    icon: <Coins className="w-5 h-5" />,
    tier: "Enhanced",
    stake: "***",
    multiplier: "***",
    description: "Higher stake unlocks enhanced reward multiplier",
  },
  {
    icon: <Vote className="w-5 h-5" />,
    tier: "Governor",
    stake: "***",
    multiplier: "***",
    description: "Top-tier rewards plus governance voting power",
  },
];

export function OpsTokenomics() {
  return (
    <>
      <Section id="tokenomics">
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Reward Structure"
              subtitle="Earn $ORAMA for every request you serve. Higher stake, higher rewards."
            />

            {/* Key metrics */}
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 max-w-4xl mx-auto w-full">
              <DashedPanel className="p-4">
                <MetricCard label="Rewards" value="$ORAMA" />
              </DashedPanel>
              <DashedPanel className="p-4">
                <MetricCard label="Payout" value="Daily" />
              </DashedPanel>
              <DashedPanel className="p-4">
                <MetricCard label="Based On" value="Uptime + Traffic" />
              </DashedPanel>
              <DashedPanel className="p-4">
                <MetricCard label="Operators" value="Unlimited" />
              </DashedPanel>
            </div>

            {/* Reward tiers */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              {rewardTiers.map((tier) => {
                const accent = TIER_ACCENT[tier.tier] ?? "#888";
                return (
                  <div
                    key={tier.tier}
                    className="group relative border border-dashed border-border p-6 flex flex-col gap-5 transition-all duration-300 hover:border-border/80"
                    style={{ borderLeftColor: accent, borderLeftWidth: 2, borderLeftStyle: "solid" }}
                  >
                    {/* Subtle gradient hover overlay */}
                    <div
                      className="absolute inset-0 opacity-0 group-hover:opacity-100 transition-opacity duration-300 pointer-events-none"
                      style={{
                        background: `linear-gradient(135deg, ${accent}08 0%, transparent 60%)`,
                      }}
                    />

                    <div className="relative flex items-center justify-between">
                      <div className="flex items-center gap-3">
                        <div style={{ color: accent }}>{tier.icon}</div>
                        <div className="flex flex-col">
                          <span className="font-display font-semibold text-fg">
                            {tier.tier}
                          </span>
                          <span className="text-xs text-muted font-mono">Tier</span>
                        </div>
                      </div>
                      <Badge variant="outline">{tier.tier}</Badge>
                    </div>

                    {/* Multiplier — large and prominent */}
                    <div className="relative flex items-baseline gap-2">
                      <span
                        className="font-mono text-4xl font-bold tracking-tight"
                        style={{ color: accent }}
                      >
                        <span className="redacted-inline">{tier.multiplier}</span>
                      </span>
                      <span className="text-xs text-muted font-mono uppercase tracking-wider">
                        Multiplier
                      </span>
                    </div>

                    <div className="relative flex items-baseline gap-1">
                      <span className="font-mono text-lg font-bold text-fg">
                        <span className="redacted-inline">{tier.stake}</span>
                      </span>
                      <span className="text-xs text-muted font-mono">$ORAMA staked</span>
                    </div>

                    <p className="relative text-sm text-muted leading-relaxed">
                      {tier.description}
                    </p>
                  </div>
                );
              })}
            </div>

            {/* Utility summary + CTA */}
            <div className="flex flex-col sm:flex-row items-center justify-between gap-4 border border-dashed border-border p-5">
              <div className="flex flex-wrap gap-6 text-sm text-muted">
                <div className="flex items-center gap-2">
                  <CreditCard className="w-4 h-4 text-accent" />
                  <span>Pay in BTC or $ORAMA</span>
                </div>
                <div className="flex items-center gap-2">
                  <Vote className="w-4 h-4 text-accent" />
                  <span>Governance voting with stake</span>
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
