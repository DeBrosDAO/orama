import { Link } from "react-router";
import { ExternalLink } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Terminal } from "../components/ui/terminal";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { SpecTable } from "../components/ui/spec-table";

const installLines = [
  { prefix: "$", text: "npm install @debros/network-ts-sdk" },
  { prefix: "", text: "" },
  { prefix: "", text: "added 12 packages in 1.2s" },
  { prefix: "", text: "" },
  { prefix: "$", text: 'cat package.json | grep network' },
  { prefix: "", text: '"@debros/network-ts-sdk": "^0.7.0"' },
];

const modules = [
  {
    name: "db",
    label: "Distributed SQL",
    code: `await client.db.query('SELECT * FROM users')
await client.db.exec('INSERT INTO ...', [args])`,
  },
  {
    name: "pubsub",
    label: "Real-Time Messaging",
    code: `await client.pubsub.subscribe('events', {
  onMessage: (msg) => console.log(msg)
})`,
  },
  {
    name: "vault",
    label: "Secret Storage",
    code: `await client.vault.store('api-key', value)
const secret = await client.vault.retrieve('api-key')`,
  },
  {
    name: "cache",
    label: "Distributed Cache",
    code: `await client.cache.set('session:123', data)
const val = await client.cache.get('session:123')`,
  },
  {
    name: "storage",
    label: "File Storage",
    code: `await client.storage.upload('avatar.png', file)
const url = await client.storage.getUrl('avatar.png')`,
  },
  {
    name: "functions",
    label: "Serverless WASM",
    code: `const result = await client.functions.execute(
  'resize-image', { width: 800 }
)`,
  },
  {
    name: "ai",
    label: "AI Marketplace",
    code: `const response = await client.ai.call(
  'model-name', { prompt: 'Hello' }
)
// Deploy an Angel (autonomous AI agent)
await client.ai.deployAngel('my-agent', wasmBundle)`,
  },
  {
    name: "bridge",
    label: "BTC Bridge",
    code: `// Deposit BTC to Orama
const deposit = await client.bridge.deposit(
  { amount: '0.01', from: 'btc' }
)
// Withdraw back to Bitcoin mainnet
await client.bridge.withdraw({ amount: '0.005' })`,
  },
  {
    name: "dex",
    label: "Native DEX",
    code: `await client.dex.placeOrder({
  pair: 'ORAMA/BTC', side: 'buy',
  amount: '100', price: '0.000001'
})
const book = await client.dex.getOrderbook('ORAMA/BTC')`,
  },
];

const quickstartLines = [
  { prefix: "$", text: "cat app.ts" },
  { prefix: "", text: "" },
  { prefix: "", text: "import { OramaClient } from '@debros/network-ts-sdk'" },
  { prefix: "", text: "" },
  { prefix: "", text: "const client = new OramaClient({" },
  { prefix: "", text: "  gateway: 'https://gateway.orama.network'," },
  { prefix: "", text: "  apiKey: 'ak_your-key:namespace'" },
  { prefix: "", text: "})" },
  { prefix: "", text: "" },
  { prefix: "", text: "// Check connection" },
  { prefix: "", text: "const health = await client.network.health()" },
  { prefix: "", text: "console.log(health) // { status: 'ok', peers: 5 }" },
  { prefix: "", text: "" },
  { prefix: "", text: "// Create a table" },
  { prefix: "", text: "await client.db.createTable(`" },
  { prefix: "", text: "  CREATE TABLE IF NOT EXISTS users (" },
  { prefix: "", text: "    id INTEGER PRIMARY KEY," },
  { prefix: "", text: "    name TEXT NOT NULL," },
  { prefix: "", text: "    email TEXT UNIQUE" },
  { prefix: "", text: "  )" },
  { prefix: "", text: "`)" },
  { prefix: "", text: "" },
  { prefix: "", text: "// Insert and query" },
  { prefix: "", text: "await client.db.exec(" },
  { prefix: "", text: "  'INSERT INTO users (name, email) VALUES (?, ?)'," },
  { prefix: "", text: "  ['Alice', 'alice@example.com']" },
  { prefix: "", text: ")" },
  { prefix: "", text: "" },
  { prefix: "", text: "const users = await client.db.query('SELECT * FROM users')" },
];

const featureRows = [
  { label: "Platform", value: "Node.js + Browser (isomorphic)" },
  { label: "Auth", value: "API keys + JWT with auto-refresh" },
  { label: "Database", value: "QueryBuilder + Repository pattern + transactions" },
  { label: "PubSub", value: "WebSocket with auto-reconnection + presence" },
  { label: "Network", value: "Health, peers, anonymous proxy (Orama Proxy)" },
  { label: "Type Safety", value: "Full TypeScript with generics" },
];

export default function SdkPage() {
  return (
    <Page title="SDK — TypeScript Client for Orama Network">
      {/* Hero */}
      <Section padding="wide">
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-12 lg:gap-16 items-center">
          <div className="flex flex-col gap-6">
            <Badge variant="default" className="w-fit">
              SDK.CLIENT.V0.7
            </Badge>

            <h1 className="font-display font-bold text-4xl lg:text-5xl leading-tight text-fg">
              One import.
              <br />
              <span className="text-accent">Every service.</span>
            </h1>

            <p className="text-muted text-lg leading-relaxed max-w-xl">
              SQL databases, key-value stores, file storage, real-time
              messaging, serverless functions, and WebRTC — all from a single
              TypeScript client. Browser and Node.js. Wallet auth. Type-safe.
              Zero configuration.
            </p>

            <div className="flex flex-wrap items-center gap-3 pt-2">
              <Button asChild size="lg">
                <Link to="/docs">Get Started</Link>
              </Button>
              <Button asChild variant="ghost" size="lg">
                <a
                  href="https://github.com/DeBrosOfficial/network-ts-sdk"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  GitHub
                  <ExternalLink className="w-3.5 h-3.5 ml-2" />
                </a>
              </Button>
            </div>
          </div>

          <Terminal lines={installLines} />
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Service Modules */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="Service Modules" />

          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-6">
            {modules.map((mod) => (
              <DashedPanel key={mod.name} withBackground>
                <div className="flex flex-col gap-3">
                  <Badge variant="outline" className="w-fit">
                    {mod.name}
                  </Badge>
                  <h3 className="font-display font-semibold text-fg">
                    {mod.label}
                  </h3>
                  <pre className="font-mono text-xs text-muted leading-relaxed overflow-x-auto">
                    {mod.code}
                  </pre>
                </div>
              </DashedPanel>
            ))}
          </div>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Quick Start */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="Quick Start" />
          <Terminal lines={quickstartLines} />
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Features */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="SDK Features" />
          <SpecTable rows={featureRows} />
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
              Start building in minutes.
            </h2>
            <Button asChild size="lg">
              <Link to="/docs">Read the Docs</Link>
            </Button>
          </div>
        </DashedPanel>
      </Section>
    </Page>
  );
}
