import { cn } from "../../lib/utils";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { DashedPanel } from "../ui/dashed-panel";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

const comparisons = [
  {
    aspect: "Data ownership",
    legacy: "Stored on their servers",
    orama: "Encrypted on distributed nodes you choose",
  },
  {
    aspect: "Vendor lock-in",
    legacy: "Deep. Migration is painful.",
    orama: "None. Fully open source. Self-hostable.",
  },
  {
    aspect: "Billing",
    legacy: "Complex. Surprise charges.",
    orama: "Free tier + Pay in $ORAMA. Pay in crypto.",
  },
  {
    aspect: "Privacy",
    legacy: "They can read your data.",
    orama: "E2E encrypted. Orama Proxy routing.",
  },
  {
    aspect: "Setup time",
    legacy: "Hours — VPCs, IAM, YAML configs",
    orama: "Minutes. One SDK. One CLI command.",
  },
  {
    aspect: "Authentication",
    legacy: "Email, password, OAuth, IAM roles",
    orama: "Wallet-based. No passwords. No PII.",
  },
];

export function DevComparison() {
  return (
    <>
      <Section>
        <AnimateIn>
        <div className="flex flex-col gap-8">
          <SectionHeader
            title="What changes when you leave the cloud."
            subtitle="Traditional clouds rent you infrastructure you never own. Orama gives you the same capabilities on infrastructure owned by the community."
          />

          {/* Desktop table layout */}
          <DashedPanel className="hidden sm:block" withCorners>
            {/* Header row */}
            <div className="grid grid-cols-[1fr_1fr_1fr] border-b border-dashed border-border">
              <div className="p-4 sm:p-5" />
              <div className="p-4 sm:p-5 border-l border-dashed border-border">
                <span className="font-mono text-xs tracking-wider uppercase text-muted">
                  Traditional Cloud
                </span>
              </div>
              <div className="p-4 sm:p-5 border-l border-dashed border-border">
                <span className="font-mono text-xs tracking-wider uppercase text-accent">
                  Orama Network
                </span>
              </div>
            </div>

            {/* Data rows */}
            {comparisons.map((row, i) => (
              <div
                key={row.aspect}
                className={cn(
                  "grid grid-cols-[1fr_1fr_1fr]",
                  i < comparisons.length - 1 &&
                    "border-b border-dashed border-border",
                )}
              >
                <div className="p-4 sm:p-5">
                  <span className="font-display font-semibold text-fg text-sm">
                    {row.aspect}
                  </span>
                </div>
                <div className="p-4 sm:p-5 border-l border-dashed border-border">
                  <span className="text-sm text-muted line-through decoration-muted/40">
                    {row.legacy}
                  </span>
                </div>
                <div className="p-4 sm:p-5 border-l border-dashed border-border">
                  <span className="text-sm text-fg">{row.orama}</span>
                </div>
              </div>
            ))}
          </DashedPanel>

          {/* Mobile card layout */}
          <div className="flex flex-col gap-4 sm:hidden">
            {comparisons.map((row) => (
              <DashedPanel key={row.aspect} className="p-4">
                <span className="font-display font-semibold text-fg text-sm block mb-3">
                  {row.aspect}
                </span>
                <div className="flex flex-col gap-2">
                  <div className="flex flex-col gap-0.5">
                    <span className="font-mono text-[10px] tracking-wider uppercase text-muted/60">
                      Traditional
                    </span>
                    <span className="text-sm text-muted line-through decoration-muted/40">
                      {row.legacy}
                    </span>
                  </div>
                  <div className="flex flex-col gap-0.5">
                    <span className="font-mono text-[10px] tracking-wider uppercase text-accent">
                      Orama
                    </span>
                    <span className="text-sm text-fg">{row.orama}</span>
                  </div>
                </div>
              </DashedPanel>
            ))}
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
