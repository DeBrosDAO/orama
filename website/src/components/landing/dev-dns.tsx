import { Globe, Lock, Link2 } from "lucide-react";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { Terminal } from "../ui/terminal";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

export function DevDns() {
  return (
    <>
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Your domain. Zero configuration."
              subtitle="Point your nameservers to Orama and every deployment gets a domain automatically. No Cloudflare. No DNS propagation headaches."
            />

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-8 lg:gap-12">
              {/* Left — features */}
              <div className="flex flex-col gap-6">
                <div className="flex items-start gap-3">
                  <Globe className="w-5 h-5 text-accent shrink-0 mt-0.5" />
                  <div>
                    <p className="font-display font-semibold text-fg text-sm">Orama Nameservers</p>
                    <p className="text-sm text-muted">
                      Point to ns1.orama.network and ns2.orama.network. Your app gets a subdomain instantly.
                    </p>
                  </div>
                </div>
                <div className="flex items-start gap-3">
                  <Lock className="w-5 h-5 text-accent shrink-0 mt-0.5" />
                  <div>
                    <p className="font-display font-semibold text-fg text-sm">Automatic TLS</p>
                    <p className="text-sm text-muted">
                      Every domain gets a valid certificate. No manual cert management. No renewal headaches.
                    </p>
                  </div>
                </div>
                <div className="flex items-start gap-3">
                  <Link2 className="w-5 h-5 text-accent shrink-0 mt-0.5" />
                  <div>
                    <p className="font-display font-semibold text-fg text-sm">Custom Domains</p>
                    <p className="text-sm text-muted">
                      Add any domain via CLI or SDK. CNAME to your Orama app and it just works.
                    </p>
                  </div>
                </div>
              </div>

              {/* Right — terminal */}
              <Terminal
                lines={[
                  { prefix: "$", text: "orama domain add myapp.com" },
                  { prefix: "\u2192", text: "CNAME myapp.com \u2192 my-app.orama.network" },
                  { prefix: "\u2192", text: "TLS certificate provisioned" },
                  { prefix: "\u2713", text: "Domain active \u2014 https://myapp.com" },
                ]}
              />
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
