import {
  Terminal,
  Globe,
  Database,
  Zap,
  Globe2,
  HardDrive,
  Lock,
  KeyRound,
  Code,
  Cpu,
  Shield,
  Network,
} from "lucide-react";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { DashedPanel } from "../ui/dashed-panel";
import { Badge } from "../ui/badge";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

const services = [
  { icon: Terminal, title: "CLI", description: "Node management, deployment, monitoring", tech: "Go" },
  { icon: Globe, title: "Gateway", description: "API gateway, routing, auth, rate limiting", tech: "Go" },
  { icon: Database, title: "RQLite", description: "Distributed SQL with Raft consensus", tech: "Go" },
  { icon: Zap, title: "Olric", description: "In-memory distributed cache", tech: "Go" },
  { icon: Globe2, title: "CoreDNS", description: "Distributed DNS, custom domains", tech: "Go" },
  { icon: HardDrive, title: "IPFS", description: "Content-addressed file storage", tech: "Go" },
  { icon: Lock, title: "Vault", description: "Secrets management, Shamir's SSS", tech: "Zig" },
  { icon: KeyRound, title: "RootWallet", description: "Multi-chain wallet authentication", tech: "TypeScript" },
  { icon: Code, title: "TypeScript SDK", description: "Client library for all services", tech: "TypeScript" },
  { icon: Cpu, title: "WASM Runtime", description: "Serverless function execution", tech: "Go" },
  { icon: Shield, title: "WireGuard", description: "Encrypted overlay network mesh", tech: "Go" },
  { icon: Network, title: "Cluster", description: "Node discovery, health, Hetzner API", tech: "Go" },
];

export function ContribServices() {
  return (
    <>
      <Section id="services">
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="What you'll work on."
              subtitle="Orama is a distributed system made of 12+ independent services. Pick what excites you."
            />

            <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-0">
              {services.map((service) => {
                const Icon = service.icon;
                return (
                  <DashedPanel key={service.title} className="p-4 sm:p-5">
                    <div className="flex flex-col gap-3">
                      <div className="flex items-center gap-2">
                        <Icon className="w-4 h-4 text-accent" />
                        <span className="font-display font-semibold text-fg text-sm">
                          {service.title}
                        </span>
                      </div>
                      <p className="text-xs text-muted leading-relaxed">
                        {service.description}
                      </p>
                      <Badge variant="outline" className="w-fit text-[10px]">
                        {service.tech}
                      </Badge>
                    </div>
                  </DashedPanel>
                );
              })}
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
