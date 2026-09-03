import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { Terminal } from "../ui/terminal";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "../ui/tabs";
import { AnimateIn } from "../ui/animate-in";
import {
  ReactLogo,
  NextjsLogo,
  GoLogo,
  NodejsLogo,
  WasmLogo,
} from "../icons/tech-logos";
import type { TerminalLine } from "../ui/terminal";

interface DeployTab {
  id: string;
  label: string;
  lines: TerminalLine[];
}

const deployTabs: DeployTab[] = [
  {
    id: "static",
    label: "React / Static",
    lines: [
      { prefix: "$", text: "orama deploy static ./dist --name my-app" },
      { prefix: "\u2192", text: "Uploading to IPFS... done" },
      { prefix: "\u2192", text: "Pinned across the cluster" },
      { prefix: "\u2713", text: "Live at https://my-app-f3o4if.orama-devnet.network" },
    ],
  },
  {
    id: "nextjs",
    label: "Next.js SSR",
    lines: [
      { prefix: "$", text: "orama deploy nextjs . --name my-next --ssr" },
      { prefix: "\u2192", text: "Building standalone output..." },
      { prefix: "\u2192", text: "Deploying across the cluster" },
      { prefix: "\u2713", text: "SSR running at https://my-next-k8x2mq.orama-devnet.network" },
    ],
  },
  {
    id: "go",
    label: "Go API",
    lines: [
      { prefix: "$", text: "orama deploy go ./cmd/api --name my-api" },
      { prefix: "\u2192", text: "Cross-compiling linux/amd64..." },
      { prefix: "\u2192", text: "Health check /health verified" },
      { prefix: "\u2713", text: "API live at https://my-api-q7v1ns.orama-devnet.network" },
    ],
  },
  {
    id: "node",
    label: "Node.js",
    lines: [
      { prefix: "$", text: "orama deploy nodejs . --name my-server" },
      { prefix: "\u2192", text: "Detecting start command..." },
      { prefix: "\u2192", text: "Deploying across the cluster" },
      { prefix: "\u2713", text: "Server running at https://my-server-b3d8pe.orama-devnet.network" },
    ],
  },
  {
    id: "wasm",
    label: "WASM Function",
    lines: [
      { prefix: "$", text: "orama function deploy --name resize" },
      { prefix: "\u2192", text: "Compiled to WebAssembly" },
      { prefix: "\u2192", text: "Deployed network-wide" },
      { prefix: "\u2713", text: "Invoke via SDK or HTTP trigger" },
    ],
  },
];

export function DevDeploy() {
  return (
    <>
      <Section id="deploy">
        <AnimateIn>
        <div className="flex flex-col gap-8">
          <SectionHeader
              title="Ship it with one command."
              subtitle="Point the CLI at a build directory or a repo. It uploads, places the app across the cluster, and hands you back a URL."
            />

          <div className="flex flex-wrap items-center gap-8">
            {[
              { Logo: ReactLogo, name: "React" },
              { Logo: NextjsLogo, name: "Next.js" },
              { Logo: GoLogo, name: "Go" },
              { Logo: NodejsLogo, name: "Node.js" },
              { Logo: WasmLogo, name: "WASM" },
            ].map(({ Logo, name }) => (
              <div key={name} className="flex flex-col items-center gap-2">
                <Logo className="w-8 h-8 text-muted" aria-hidden="true" />
                <span className="text-xs font-mono text-muted">{name}</span>
              </div>
            ))}
          </div>

          <p className="text-muted text-base leading-relaxed max-w-2xl">
            Static sites, Next.js with SSR, Go APIs, Node.js servers and WASM
            functions — from your terminal. No dashboard to click through, no
            YAML to write, no region to pick.
          </p>

          <Tabs defaultValue="static">
            <TabsList className="flex-wrap">
              {deployTabs.map((tab) => (
                <TabsTrigger key={tab.id} value={tab.id}>
                  {tab.label}
                </TabsTrigger>
              ))}
            </TabsList>

            {deployTabs.map((tab) => (
              <TabsContent key={tab.id} value={tab.id}>
                <Terminal lines={tab.lines} />
              </TabsContent>
            ))}
          </Tabs>

          <p className="text-sm text-muted font-mono">
            Every deploy lands on the node mesh, gets a subdomain with
            automatic TLS, and is health-checked before it serves traffic.
            Custom domains are one CLI call away.
          </p>
        </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>
    </>
  );
}
