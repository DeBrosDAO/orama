import { Link } from "react-router";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { SyntaxCodeBlock } from "../ui/syntax-code-block";
import { Badge } from "../ui/badge";
import { DashedPanel } from "../ui/dashed-panel";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { Button } from "../ui/button";
import { ExternalLink } from "lucide-react";
import { AnimateIn } from "../ui/animate-in";

const sdkCode = `import { OramaClient } from '@debros/network-ts-sdk'

const client = new OramaClient({
  gateway: 'https://orama-testnet.network',
  apiKey: process.env.ORAMA_API_KEY
})

// SQL Database (like MySQL, powered by RQLite)
const users = await client.db.query(
  'SELECT * FROM users WHERE active = true'
)

// Key-Value Cache (like Redis, powered by Olric)
await client.kv.set('session:abc', { theme: 'dark' }, { ttl: 3600 })

// Real-Time Messaging
client.pubsub.subscribe('chat:lobby', (msg) => {
  console.log(msg)
})

// File Storage (IPFS)
const cid = await client.storage.upload(myFile)

// Serverless Functions (WASM)
await client.functions.invoke('resize-image', { cid, width: 800 })`;

const setupSteps = [
  "Connect with Root Wallet",
  "Get your API key from the dashboard",
  "Import the SDK and start building",
];

export function DevQuickstart() {
  return (
    <>
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="One SDK. Every service."
              subtitle="Login with your wallet. Get your API key. That's your entire setup. No databases to provision, no servers to configure."
            />

            {/* Setup steps */}
            <DashedPanel withBackground className="max-w-2xl mx-auto w-full">
              <div className="flex flex-col gap-3 p-4 sm:p-6">
                {setupSteps.map((step, i) => (
                  <div key={step} className="flex items-center gap-3">
                    <Badge variant="outline" className="shrink-0 font-mono">
                      {i + 1}
                    </Badge>
                    <p className="text-sm text-fg">{step}</p>
                  </div>
                ))}
              </div>
            </DashedPanel>

            {/* Code block */}
            <SyntaxCodeBlock code={sdkCode} label="FIG.02 — ORAMACLIENT USAGE" />

            {/* Explainer */}
            <p className="text-sm text-muted max-w-2xl mx-auto text-center">
              RQLite is our distributed SQL database — like MySQL but with Raft consensus and automatic failover.
              Olric is our distributed cache — like Redis but built into every node. They're already running on the network. You just use them.
            </p>

            {/* Links */}
            <div className="flex flex-wrap items-center justify-center gap-4">
              <p className="text-sm text-muted">
                Works with React, Next.js, Node.js, and any JavaScript runtime.
              </p>
              <div className="flex items-center gap-3">
                <Button asChild variant="link" size="sm">
                  <Link to="/docs/developer/sdk">SDK Docs</Link>
                </Button>
                <Button asChild variant="link" size="sm">
                  <a
                    href="https://github.com/DeBrosOfficial/network-ts-sdk"
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    GitHub
                    <ExternalLink className="w-3 h-3 ml-1" />
                  </a>
                </Button>
              </div>
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
