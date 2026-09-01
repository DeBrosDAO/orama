import { Link } from "react-router";
import { ArrowRight, ExternalLink, BookOpen, Terminal as TerminalIcon, Code } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { Button } from "../components/ui/button";
import { DashedPanel } from "../components/ui/dashed-panel";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";
import { Terminal } from "../components/ui/terminal";
import { HeroMesh } from "../components/landing/hero-mesh";
import { DevFeatures } from "../components/landing/dev-features";
import { NodeAnatomy } from "../components/landing/node-anatomy";
import { Privacy } from "../components/landing/privacy";
import { DevDeploy } from "../components/landing/dev-deploy";
import { DevDns } from "../components/landing/dev-dns";
import { CliInstall } from "../components/landing/cli-install";
import { GITHUB_URL } from "../data/navigation";

/* ═══════════════════════════════════════════
   1. HERO
   ═══════════════════════════════════════════ */
function HomeHero() {
  return (
    // -mt-16 pulls the hero under the fixed navbar so the mesh fills the
    // viewport edge to edge; the inner padding keeps the copy clear of it.
    <section className="relative -mt-16 min-h-[100svh] flex items-center justify-center overflow-hidden">
      <HeroMesh />

      <div className="relative z-10 flex flex-col items-center text-center gap-6 max-w-3xl mx-auto px-4 sm:px-6 pt-28 pb-20">
        <span className="inline-flex items-center gap-2 px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full border border-dashed border-border text-muted">
          <span className="w-1.5 h-1.5 rounded-full bg-amber-400" />
          Alpha — devnet &amp; testnet
        </span>

        <h1 className="font-display font-bold text-[2.15rem] leading-[1.08] sm:text-5xl lg:text-6xl tracking-tight text-fg text-balance">
          The cloud, with
          <br />
          nobody in the middle.
        </h1>

        <p className="text-muted text-base sm:text-lg leading-relaxed max-w-xl text-pretty">
          Orama is a decentralized, privacy-first alternative to the big clouds.
          The same primitives you rent from them — SQL, storage, cache, pubsub,
          functions, deployments — running on a mesh of independently operated
          nodes.
        </p>

        <div className="flex flex-col sm:flex-row w-full sm:w-auto items-stretch sm:items-center gap-3 justify-center pt-2">
          <Button asChild size="lg">
            <Link to="/docs/developer/getting-started">
              Read the docs
              <ArrowRight className="w-3.5 h-3.5 ml-2" />
            </Link>
          </Button>
          <Button asChild variant="ghost" size="lg">
            <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer">
              View the source
              <ExternalLink className="w-3.5 h-3.5 ml-2" />
            </a>
          </Button>
        </div>

        <p className="text-xs font-mono text-muted max-w-md pt-1 leading-relaxed">
          Alpha. Running on devnet and testnet, not yet open for public sign-ups
          — the code and the docs are open now.
        </p>
      </div>
    </section>
  );
}

/* ═══════════════════════════════════════════
   2. WHAT IT LOOKS LIKE
   ═══════════════════════════════════════════ */
function CodeIntro() {
  return (
    <>
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="One client. Every service."
              subtitle="Authenticate with a wallet, get an API key for your namespace, and the rest is method calls."
            />

            <Terminal
              lines={[
                { prefix: "$", text: "npm install @debros/orama" },
                { text: "" },
                { prefix: "#", text: "then, in your app" },
                { text: 'import { createClient } from "@debros/orama";' },
                { text: "" },
                { text: "const client = createClient({" },
                { text: '  baseURL: "https://your-node.orama-devnet.network",' },
                { text: '  apiKey: "your-api-key",' },
                { text: "});" },
                { text: "" },
                {
                  text: 'await client.db.query("SELECT * FROM users WHERE name = ?", ["Alice"]);',
                },
              ]}
            />
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>
    </>
  );
}

/* ═══════════════════════════════════════════
   3. DOCS
   ═══════════════════════════════════════════ */
const DOC_LINKS = [
  {
    title: "Getting Started",
    description:
      "Install the CLI, authenticate with a wallet, deploy something.",
    href: "/docs/developer/getting-started",
    icon: <BookOpen className="w-4 h-4" />,
  },
  {
    title: "CLI Reference",
    description: "Every command, flag and environment the orama binary accepts.",
    href: "/docs/developer/cli-reference",
    icon: <TerminalIcon className="w-4 h-4" />,
  },
  {
    title: "SDK Reference",
    description: "The full TypeScript API for databases, storage, pubsub and functions.",
    href: "/docs/developer/sdk-reference",
    icon: <Code className="w-4 h-4" />,
  },
];

function DocsSection() {
  return (
    <Section>
      <AnimateIn>
        <div className="flex flex-col gap-8">
          <SectionHeader
            title="It is all written down."
            subtitle="Documentation written against what the code actually does today — developer guides, operator runbooks and contributor setup. When the two disagree, the docs get fixed."
          />

          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            {DOC_LINKS.map((doc) => (
              <Link
                key={doc.href}
                to={doc.href}
                className="group flex flex-col gap-2 p-5 border border-dashed border-border hover:border-fg/30 hover:bg-white/[0.02] transition-all duration-150"
              >
                <span className="text-accent">{doc.icon}</span>
                <span className="font-display font-semibold text-sm text-fg flex items-center gap-1.5">
                  {doc.title}
                  <ArrowRight className="w-3 h-3 opacity-0 -translate-x-1 group-hover:opacity-100 group-hover:translate-x-0 transition-all" />
                </span>
                <span className="text-sm text-muted leading-relaxed">
                  {doc.description}
                </span>
              </Link>
            ))}
          </div>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   4. FINAL CTA
   ═══════════════════════════════════════════ */
function FinalCTA() {
  return (
    <Section padding="wide">
      <AnimateIn>
        <DashedPanel withBackground withCorners className="text-center">
          <div className="flex flex-col items-center gap-5 py-6">
            <h2 className="font-display font-bold text-2xl md:text-3xl text-fg tracking-tight">
              Run a node, or read the code.
            </h2>
            <p className="text-sm text-muted max-w-lg leading-relaxed">
              Orama runs on nodes other people operate — which only works if
              those people are you. The operator docs cover installing a node and
              joining the mesh; the repository is where the work happens.
            </p>
            <div className="flex flex-wrap gap-3 justify-center pt-1">
              <Button asChild size="lg">
                <Link to="/docs/operator/getting-started">
                  Run a node
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </Link>
              </Button>
              <Button asChild variant="ghost" size="lg">
                <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer">
                  GitHub
                  <ExternalLink className="w-3.5 h-3.5 ml-2" />
                </a>
              </Button>
            </div>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}

export default function Home() {
  return (
    <Page title="The Decentralized, Privacy-First Cloud">
      <HomeHero />
      <DevFeatures />
      <NodeAnatomy />
      <Privacy />
      <CodeIntro />
      <DevDeploy />
      <CliInstall />
      <DevDns />
      <DocsSection />
      <FinalCTA />
    </Page>
  );
}
