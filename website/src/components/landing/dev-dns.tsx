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
              title="A domain and a certificate, without asking for one."
              subtitle="Every node runs CoreDNS as an authoritative nameserver for the network. Deployments get a resolvable subdomain and a valid certificate as part of the deploy."
            />

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-8 lg:gap-12">
              {/* Left — features */}
              <div className="flex flex-col gap-6">
                <div className="flex items-start gap-3">
                  <Globe className="w-5 h-5 text-accent shrink-0 mt-0.5" />
                  <div>
                    <p className="font-display font-semibold text-fg text-sm">
                      DNS served by the mesh
                    </p>
                    <p className="text-sm text-muted">
                      NS records point at the Orama nodes themselves, with glue
                      records so resolvers can reach them. Queries round-robin
                      across every nameserver node.
                    </p>
                  </div>
                </div>
                <div className="flex items-start gap-3">
                  <Lock className="w-5 h-5 text-accent shrink-0 mt-0.5" />
                  <div>
                    <p className="font-display font-semibold text-fg text-sm">
                      Certificates via DNS-01
                    </p>
                    <p className="text-sm text-muted">
                      ACME challenges are answered from CoreDNS, so any
                      nameserver can complete them. No node has to be reachable
                      at a fixed address for a certificate to issue.
                    </p>
                  </div>
                </div>
                <div className="flex items-start gap-3">
                  <Link2 className="w-5 h-5 text-accent shrink-0 mt-0.5" />
                  <div>
                    <p className="font-display font-semibold text-fg text-sm">
                      Bring your own domain
                    </p>
                    <p className="text-sm text-muted">
                      Add the domain, prove ownership with a TXT record, point
                      an A record at the deployment. The certificate is
                      provisioned for you.
                    </p>
                  </div>
                </div>
              </div>

              {/* Right — the record set */}
              <Terminal
                lines={[
                  { prefix: "#", text: "authoritative zone, served by the nodes" },
                  {
                    text: "orama-devnet.network. NS ns1.orama-devnet.network.",
                  },
                  {
                    text: "orama-devnet.network. NS ns2.orama-devnet.network.",
                  },
                  { text: "" },
                  { prefix: "#", text: "glue records" },
                  { text: "ns1.orama-devnet.network.  A  <node-1-ip>" },
                  { text: "ns2.orama-devnet.network.  A  <node-2-ip>" },
                  { text: "" },
                  { prefix: "#", text: "your deployment, resolvable on deploy" },
                  { prefix: "$", text: "dig +short my-app-f3o4if.orama-devnet.network" },
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
