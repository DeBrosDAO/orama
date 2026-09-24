import { Heart } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { CopyButton } from "../components/ui/copy-button";
import { QrCode } from "../components/ui/qr-code";
import { ROUTES } from "../content/routes";
import { WALLETS } from "../content/wallets";

export default function Donate() {
  return (
    <Page route={ROUTES.donate}>
      <PageHero
        eyebrow="Donate"
        title="Keep the people's cloud growing."
        line="Orama is open source. Donations pay for development and for the machines that run the network."
      >
        <Heart size={20} className="text-muted mt-2" />
      </PageHero>

      <Section padding="narrow">
        <ul className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {WALLETS.map((w) => (
            <li key={w.id} className="flex flex-col sm:flex-row gap-6 p-6 border border-dashed border-border bg-surface/60">
              <div className="shrink-0 self-center sm:self-start">
                <QrCode value={w.address} label={`${w.chain} donation address QR code`} size={148} />
              </div>
              <div className="flex flex-col gap-3 min-w-0 flex-1">
                <div className="flex items-baseline gap-2">
                  <h2 className="font-display font-bold text-xl text-fg">{w.chain}</h2>
                  <span className="font-mono text-xs text-muted">{w.symbol}</span>
                </div>
                <span className="font-mono text-[10px] tracking-wider uppercase text-muted">{w.network}</span>
                <code className="font-mono text-xs text-fg break-all leading-relaxed bg-surface-2/60 border border-border/60 p-3 select-all">
                  {w.address}
                </code>
                <CopyButton value={w.address} label={`Copy ${w.chain} address`} className="self-start" />
              </div>
            </li>
          ))}
        </ul>
        <p className="mt-10 text-center text-xs text-muted">
          Send only the matching coin to each address. Donations are voluntary and non-refundable.
        </p>
      </Section>
    </Page>
  );
}
