import {
  Database,
  HardDrive,
  Cpu,
  Archive,
  Radio,
  Video,
  Globe,
  Lock,
} from "lucide-react";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { FeatureCard } from "../ui/feature-card";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

const features = [
  {
    icon: <Database className="w-5 h-5" />,
    title: "Network SQL",
    subtitle: "replaces RDS / Aurora",
    description:
      "Distributed SQL with Raft consensus. ACID transactions. Automatic failover across nodes.",
  },
  {
    icon: <HardDrive className="w-5 h-5" />,
    title: "Network Cache",
    subtitle: "replaces ElastiCache",
    description:
      "In-memory key-value store with TTL, replication, and namespace isolation via Olric.",
  },
  {
    icon: <Cpu className="w-5 h-5" />,
    title: "Network Functions",
    subtitle: "replaces Lambda",
    description:
      "Serverless WebAssembly. Write in Go, compile to WASM, deploy network-wide.",
  },
  {
    icon: <Archive className="w-5 h-5" />,
    title: "Network Storage",
    subtitle: "replaces S3 / R2",
    description:
      "Content-addressed storage on IPFS. Upload, pin, and retrieve from any node.",
  },
  {
    icon: <Radio className="w-5 h-5" />,
    title: "Network PubSub",
    subtitle: "replaces SNS / SQS",
    description:
      "Real-time topic-based messaging with WebSocket native support.",
  },
  {
    icon: <Video className="w-5 h-5" />,
    title: "Network WebRTC",
    subtitle: "replaces Twilio / Daily",
    description:
      "SFU + TURN servers for video, audio, and data channels.",
  },
  {
    icon: <Globe className="w-5 h-5" />,
    title: "Network DNS",
    subtitle: "replaces Route53",
    description:
      "CoreDNS distributed across the mesh. Custom domains built in.",
  },
  {
    icon: <Lock className="w-5 h-5" />,
    title: "Network Vault",
    subtitle: "replaces Secrets Manager",
    description:
      "Shamir's Secret Sharing. Secrets split across nodes. No single point of compromise.",
  },
];

export function DevFeatures() {
  return (
    <>
      <Section>
        <AnimateIn>
        <div className="flex flex-col gap-8">
          <SectionHeader
            title="A complete cloud. Zero infrastructure."
            subtitle="No databases to provision. No cache to configure. Import the SDK and you have everything."
          />

          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-0">
            {features.map((feature) => (
              <FeatureCard
                key={feature.title}
                icon={feature.icon}
                title={feature.title}
                subtitle={feature.subtitle}
                description={feature.description}
              />
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
