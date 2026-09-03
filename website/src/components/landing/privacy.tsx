import { Fingerprint, EyeOff, Lock, Network } from "lucide-react";
import type { ReactNode } from "react";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

interface PrivacyItem {
  icon: ReactNode;
  title: string;
  body: string;
}

const items: PrivacyItem[] = [
  {
    icon: <Fingerprint className="w-5 h-5" />,
    title: "No account to hand over",
    body: "Authentication is a wallet signature — request a nonce, sign it, get a token. There is no email, no password and no profile, because there is nothing to collect.",
  },
  {
    icon: <EyeOff className="w-5 h-5" />,
    title: "Requests that don't carry your IP",
    body: "Outbound HTTP can be routed through the Anyone relay network, so the service you call sees the relay rather than you or your users.",
  },
  {
    icon: <Network className="w-5 h-5" />,
    title: "Calls that don't leak peers",
    body: "WebRTC runs in forced-relay mode, enforced server-side. Two people on a call exchange media through the network instead of learning each other's addresses.",
  },
  {
    icon: <Lock className="w-5 h-5" />,
    title: "A network that talks to itself privately",
    body: "Every node-to-node hop travels an encrypted WireGuard mesh. Internal services bind to overlay addresses and are not reachable from the public internet at all.",
  },
];

export function Privacy() {
  return (
    <>
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Privacy is the default, not a plan."
              subtitle="The big clouds are a single operator who can read your data, log your traffic and identify your users. Orama is built so that there is no such operator."
            />

            <div className="grid grid-cols-1 md:grid-cols-2 gap-px bg-border/40 border border-dashed border-border">
              {items.map((item) => (
                <div
                  key={item.title}
                  className="flex flex-col gap-3 bg-surface p-6"
                >
                  <span className="text-accent">{item.icon}</span>
                  <h3 className="font-display font-semibold text-fg text-sm">
                    {item.title}
                  </h3>
                  <p className="text-sm text-muted leading-relaxed">
                    {item.body}
                  </p>
                </div>
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
