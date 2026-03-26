import { Section } from "../layout/section";
import { DashedPanel } from "../ui/dashed-panel";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

export function OpsOramaOne() {
  return (
    <>
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-12">
            {/* Header */}
            <div className="flex flex-col items-center text-center gap-4">
              <Badge variant="status">COMING SOON</Badge>
              <h2 className="font-display text-3xl lg:text-4xl font-bold text-fg tracking-tight">
                Orama One
              </h2>
              <p className="text-lg text-accent font-mono tracking-wider">
                Plug in. Connect. Earn.
              </p>
            </div>

            {/* Device showcase */}
            <DashedPanel withCorners className="w-full overflow-hidden">
              <div className="relative flex items-center justify-center py-16 sm:py-24">
                {/* Ambient glow */}
                <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
                  <div className="w-64 h-64 sm:w-96 sm:h-96 rounded-full bg-accent/[0.06] blur-[80px]" />
                </div>

                {/* Device silhouette */}
                <div className="relative w-72 sm:w-96 h-32 sm:h-40">
                  {/* Main body */}
                  <div className="absolute inset-0 bg-bg rounded-xl border border-border/60 shadow-[0_0_60px_rgba(65,105,225,0.1),0_0_120px_rgba(65,105,225,0.05)]" />

                  {/* Top edge highlight */}
                  <div className="absolute top-0 left-4 right-4 h-px bg-gradient-to-r from-transparent via-accent/30 to-transparent" />

                  {/* LED indicators */}
                  <div className="absolute top-5 left-6 flex gap-3">
                    <div className="w-2 h-2 rounded-full bg-border/60" />
                    <div className="w-2 h-2 rounded-full bg-border/60" />
                    <div className="w-2 h-2 rounded-full bg-accent/50 animate-pulse" />
                  </div>

                  {/* Ventilation lines */}
                  <div className="absolute top-5 right-6 flex flex-col gap-1.5">
                    <div className="w-8 h-px bg-border/40" />
                    <div className="w-8 h-px bg-border/40" />
                    <div className="w-8 h-px bg-border/40" />
                  </div>

                  {/* Center wordmark */}
                  <div className="absolute inset-0 flex items-center justify-center">
                    <span className="text-sm sm:text-base font-mono text-muted/20 tracking-[0.4em] uppercase">
                      Orama One
                    </span>
                  </div>

                  {/* Bottom ports */}
                  <div className="absolute bottom-4 left-1/2 -translate-x-1/2 flex gap-4">
                    <div className="w-6 h-2 rounded-sm border border-border/40" />
                    <div className="w-6 h-2 rounded-sm border border-border/40" />
                    <div className="w-3 h-2 rounded-full border border-border/40" />
                  </div>

                  {/* Base stand */}
                  <div className="absolute -bottom-3 left-1/2 -translate-x-1/2 w-48 sm:w-64 h-1 bg-border/20 rounded-full" />
                </div>
              </div>
            </DashedPanel>

            {/* Specs */}
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 max-w-4xl mx-auto w-full">
              <DashedPanel className="p-4 text-center">
                <span className="text-xs font-mono text-muted block mb-1">FORM FACTOR</span>
                <span className="text-sm text-fg">Compact. Silent. Always-on.</span>
              </DashedPanel>
              <DashedPanel className="p-4 text-center">
                <span className="text-xs font-mono text-muted block mb-1">CONNECTIVITY</span>
                <span className="text-sm text-fg">Ethernet + WiFi + WireGuard</span>
              </DashedPanel>
              <DashedPanel className="p-4 text-center">
                <span className="text-xs font-mono text-muted block mb-1">SETUP</span>
                <span className="text-sm text-fg">Plug in and start earning</span>
              </DashedPanel>
            </div>

            {/* Description + CTA */}
            <div className="flex flex-col items-center text-center gap-6 max-w-2xl mx-auto">
              <p className="text-muted leading-relaxed">
                A pre-built hardware node. No VPS. No terminal. No configuration.
                Just plug it in, connect to the network, and start earning $ORAMA.
              </p>
              <Button variant="ghost" size="lg">
                Notify Me When Available
              </Button>
              <p className="text-xs font-mono text-muted">
                Expected Q2 2026
              </p>
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
