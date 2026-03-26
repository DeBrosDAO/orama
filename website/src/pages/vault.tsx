import { Link } from "react-router";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { CodeBlock } from "../components/ui/code-block";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { StatusRow } from "../components/ui/status-row";
import { SpecTable } from "../components/ui/spec-table";

const shamirDiagram = `Secret: "my-database-password"
      \u2193 Shamir Split (K=3, N=5)

Node A: [share-1] \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2591\u2591
Node B: [share-2] \u2591\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2591
Node C: [share-3] \u2591\u2591\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588
Node D: [share-4] \u2588\u2588\u2588\u2591\u2591\u2591\u2588\u2588\u2588\u2588
Node E: [share-5] \u2588\u2588\u2588\u2588\u2591\u2591\u2591\u2588\u2588\u2588

Any 3 shares \u2192 reconstruct secret \u2713
Any 2 shares \u2192 zero information \u2717`;

const steps = [
  {
    number: "01",
    title: "Split",
    description:
      "Your secret is mathematically split into N shares using polynomial interpolation over GF(2^8). Each share is meaningless alone.",
  },
  {
    number: "02",
    title: "Distribute",
    description:
      "Shares are encrypted with AES-256-GCM and distributed to guardian nodes across the network. Every node stores exactly one share.",
  },
  {
    number: "03",
    title: "Reconstruct",
    description:
      "When you need your secret, K shares are collected from guardians and combined. The threshold K adapts to cluster size: K = max(3, \u230AN/3\u230B).",
  },
];

const vaultFeatures = [
  {
    name: "Authentication",
    type: "auth / hmac",
    description: "Challenge-response with HMAC-SHA256 session tokens",
  },
  {
    name: "Named Secrets",
    type: "api / v2",
    description: "Full CRUD operations on named secrets with session auth",
  },
  {
    name: "Peer Protocol",
    type: "mesh / tcp",
    description: "Binary TCP protocol for inter-guardian communication on WireGuard",
  },
  {
    name: "Heartbeat",
    type: "health / 5s",
    description: "Continuous peer monitoring with alive/suspect/dead states",
  },
  {
    name: "Repair",
    type: "resilience / auto",
    description: "Proactive re-sharing when guardians join or leave the cluster",
  },
  {
    name: "Storage",
    type: "disk / atomic",
    description: "File-per-user with atomic writes, no database dependency",
  },
];

const sdkCode = `import { OramaClient } from '@debros/network-ts-sdk'

const client = new OramaClient({ gateway, apiKey })

// Store a secret
await client.vault.store('db-password', 'super-secret-123')

// Retrieve it
const secret = await client.vault.retrieve('db-password')

// List all secrets
const secrets = await client.vault.list()`;

const securityRows = [
  { label: "Encryption", value: "AES-256-GCM per share" },
  { label: "Key Derivation", value: "HKDF-SHA256" },
  { label: "Integrity", value: "HMAC-SHA256 verification" },
  { label: "Field Arithmetic", value: "GF(2^8), same as AES" },
  { label: "Fault Tolerance", value: "Can lose N-K nodes (e.g., 14 nodes \u2192 lose 10)" },
  { label: "Max Share Size", value: "512 KiB" },
  { label: "Max Secrets", value: "1,000 per identity" },
];

export default function VaultPage() {
  return (
    <Page title="Vault — Zero-Knowledge Secret Storage">
      {/* Hero */}
      <Section padding="wide">
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-12 lg:gap-16 items-center">
          <div className="flex flex-col gap-6">
            <Badge variant="default" className="w-fit">
              VAULT.ENGINE.V1
            </Badge>

            <h1 className="font-display font-bold text-4xl lg:text-5xl leading-tight text-fg">
              Your secrets.
              <br />
              <span className="text-accent">Split across the network.</span>
            </h1>

            <p className="text-muted text-lg leading-relaxed max-w-xl">
              Vault uses Shamir&apos;s Secret Sharing to split your encryption
              keys and sensitive data across multiple nodes. No single node
              — and no single person — can access your secrets. Reconstruct
              only when you need them, with threshold-based recovery.
            </p>

            <div className="flex flex-wrap items-center gap-3 pt-2">
              <Button asChild size="lg">
                <Link to="/playground">Try Vault</Link>
              </Button>
              <Button asChild variant="ghost" size="lg">
                <Link to="/docs">Read Docs</Link>
              </Button>
            </div>
          </div>

          <CodeBlock label="FIG.01 — SECRET SHARING">
            {shamirDiagram}
          </CodeBlock>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* How Shamir's Works */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="How Shamir's Works" />

          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            {steps.map((step) => (
              <DashedPanel key={step.number} withBackground>
                <div className="flex flex-col gap-3">
                  <span className="font-mono text-xs text-accent tracking-wider">
                    STEP {step.number}
                  </span>
                  <h3 className="font-display font-semibold text-fg text-lg">
                    {step.title}
                  </h3>
                  <p className="text-sm text-muted leading-relaxed">
                    {step.description}
                  </p>
                </div>
              </DashedPanel>
            ))}
          </div>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Features */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="Vault Features" />

          <DashedPanel className="divide-y divide-dashed divide-border">
            {vaultFeatures.map((feature) => (
              <StatusRow
                key={feature.name}
                name={feature.name}
                type={feature.type}
                description={feature.description}
                status="active"
                className="px-4 sm:px-6"
              />
            ))}
          </DashedPanel>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* SDK */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="Developer Access" />
          <CodeBlock label="SDK USAGE">{sdkCode}</CodeBlock>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Security */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="Security Properties" />
          <SpecTable rows={securityRows} />
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* CTA */}
      <Section padding="wide">
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col items-center text-center gap-6 py-8">
            <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
              Secrets that can&apos;t be stolen.
            </h2>
            <Button asChild size="lg">
              <Link to="/playground">Explore Vault</Link>
            </Button>
          </div>
        </DashedPanel>
      </Section>
    </Page>
  );
}
