import { Link } from "react-router";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { Terminal } from "../ui/terminal";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

export function OpsSetup() {
  return (
    <>
      <Section id="setup">
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="From zero to earning in 5 minutes."
              subtitle="Testnet is free — no staking required. Get a VPS, install OramaOS, and start earning $ORAMA that carries over to mainnet."
            />

            <div className="flex flex-col gap-8 max-w-3xl mx-auto w-full">
              {/* Step 1 */}
              <div className="flex items-start gap-4">
                <Badge variant="outline" className="shrink-0 mt-1 font-mono">1</Badge>
                <div>
                  <h3 className="font-display font-semibold text-fg text-sm mb-1">Get a VPS</h3>
                  <p className="text-sm text-muted">
                    Cloud: 2+ vCPU, 4GB RAM, 80GB SSD. Standard: 4+ cores, 8GB RAM, 256GB NVMe, TPM 2.0. Hetzner, DigitalOcean, Vultr — any provider works.
                  </p>
                </div>
              </div>

              {/* Step 2 */}
              <div className="flex items-start gap-4">
                <Badge variant="outline" className="shrink-0 mt-1 font-mono">2</Badge>
                <div className="flex-1">
                  <h3 className="font-display font-semibold text-fg text-sm mb-2">Install the Orama node</h3>
                  <Terminal
                    lines={[
                      { prefix: "$", text: "brew install DeBrosOfficial/tap/orama" },
                      { prefix: "\u2192", text: "Installing Orama CLI... done" },
                      { text: "" },
                      { prefix: "$", text: "sudo orama node install --vps-ip <IP> --domain <domain> --token <invite>" },
                      { prefix: "\u2192", text: "Downloading services... done" },
                      { prefix: "\u2192", text: "Configuring WireGuard mesh..." },
                      { prefix: "\u2192", text: "Starting RQLite, Olric, Gateway..." },
                      { prefix: "\u2713", text: "Node is live and connected to the mesh" },
                    ]}
                  />
                </div>
              </div>

              {/* Step 3 */}
              <div className="flex items-start gap-4">
                <Badge variant="outline" className="shrink-0 mt-1 font-mono">3</Badge>
                <div className="flex-1">
                  <h3 className="font-display font-semibold text-fg text-sm mb-2">Verify and start earning</h3>
                  <Terminal
                    lines={[
                      { prefix: "$", text: "orama node status" },
                      { prefix: "\u2192", text: "Node: online" },
                      { prefix: "\u2192", text: "Cluster: connected (3/3 peers)" },
                      { prefix: "\u2713", text: "Rewards: accumulating" },
                    ]}
                  />
                </div>
              </div>
            </div>

            <div className="flex justify-center">
              <Button asChild variant="ghost">
                <Link to="/docs/operator/node-setup">
                  Read the full setup guide &rarr;
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
