import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { SpecTable } from "../ui/spec-table";
import { DashedPanel } from "../ui/dashed-panel";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { Badge } from "../ui/badge";
import { AnimateIn } from "../ui/animate-in";
const rewardSpec = [
  { label: "Reward token", value: "$ORAMA — uptime, bandwidth, compute" },
  { label: "Network privacy", value: "Orama Proxy — onion routing for traffic obfuscation" },
  { label: "Transaction privacy", value: "PLONK zk-SNARKs — per-tx public/private toggle (core protocol)" },
  { label: "Payout", value: "Continuous, based on Effective Power formula" },
];

export function OpsAnyone() {
  return (
    <>
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader title="Privacy Built In with Orama Proxy" />

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-8 lg:gap-12">
              {/* Left — text */}
              <div className="flex flex-col gap-4">
                <Badge variant="accent" className="w-fit">Privacy Layer</Badge>
                <p className="text-muted leading-relaxed">
                  Every Orama node runs the Orama Proxy privacy relay for network-level traffic
                  obfuscation via onion routing. This is the transport layer — it hides where traffic
                  comes from. The core privacy feature is PLONK zk-SNARKs at the protocol level,
                  which provides per-transaction public/private toggle (hiding sender, receiver, and
                  amount). Private transactions cost 4x the public gas equivalent.
                </p>
                <SpecTable rows={rewardSpec} />
              </div>

              {/* Right — visual */}
              <DashedPanel withCorners withBackground>
                <div className="flex flex-col items-center justify-center gap-6 p-8">
                  <div className="w-20 h-20 rounded-full border border-dashed border-border flex items-center justify-center">
                    <span className="text-3xl font-bold font-mono text-fg">OP</span>
                  </div>
                  <div className="flex items-center gap-6">
                    <div className="text-center">
                      <div className="text-2xl font-bold text-fg font-display">$ORAMA</div>
                      <div className="text-xs font-mono text-muted mt-1">Network rewards</div>
                    </div>
                  </div>
                  <div className="w-full border-t border-dashed border-border" />
                  <p className="text-sm text-muted text-center">
                    One node. Privacy built in. Earn $ORAMA from privacy infrastructure.
                  </p>
                </div>
              </DashedPanel>
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
