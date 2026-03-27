import { useEffect, useRef } from "react";
import { Link } from "react-router";
import {
  Wallet,
  Lock,
  KeyRound,
  Link as LinkIcon,
  Terminal as TerminalIcon,
  Layers,
  Shield,
  Smartphone,
  Globe,
} from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Terminal } from "../components/ui/terminal";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { SectionHeader } from "../components/ui/section-header";
import { FeatureCard } from "../components/ui/feature-card";
import { DashedPanel } from "../components/ui/dashed-panel";
import { SpecTable } from "../components/ui/spec-table";
import { SyntaxCodeBlock } from "../components/ui/syntax-code-block";
import { AnimateIn } from "../components/ui/animate-in";

/* ── Animated slash canvas ── */
function SlashCanvas() {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    let animationId: number;
    let time = 0;

    const resize = () => {
      const dpr = window.devicePixelRatio || 1;
      canvas.width = canvas.offsetWidth * dpr;
      canvas.height = canvas.offsetHeight * dpr;
      ctx.scale(dpr, dpr);
    };
    resize();
    window.addEventListener("resize", resize);

    const draw = () => {
      const w = canvas.offsetWidth;
      const h = canvas.offsetHeight;
      ctx.clearRect(0, 0, w, h);
      time += 0.005;

      // Floating grid dots
      const spacing = 40;
      for (let x = spacing; x < w; x += spacing) {
        for (let y = spacing; y < h; y += spacing) {
          const dist = Math.sqrt((x - w / 2) ** 2 + (y - h / 2) ** 2);
          const wave = Math.sin(dist * 0.01 - time * 2) * 0.5 + 0.5;
          const opacity = wave * 0.15;
          ctx.fillStyle = `rgba(255, 255, 255, ${opacity})`;
          ctx.beginPath();
          ctx.arc(x, y, 1, 0, Math.PI * 2);
          ctx.fill();
        }
      }

      // Pulsing rings from center
      for (let i = 0; i < 3; i++) {
        const radius = ((time * 60 + i * 80) % 240);
        const opacity = Math.max(0, 1 - radius / 240) * 0.08;
        ctx.strokeStyle = `rgba(255, 255, 255, ${opacity})`;
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.arc(w / 2, h / 2, radius, 0, Math.PI * 2);
        ctx.stroke();
      }

      animationId = requestAnimationFrame(draw);
    };
    draw();

    return () => {
      window.removeEventListener("resize", resize);
      cancelAnimationFrame(animationId);
    };
  }, []);

  return (
    <canvas
      ref={canvasRef}
      className="absolute inset-0 w-full h-full pointer-events-none"
      aria-hidden="true"
    />
  );
}

const terminalLines = [
  { prefix: "$", text: "rw init" },
  { prefix: "\u2192", text: "Generating BIP-39 mnemonic... done" },
  { prefix: "\u2192", text: "Deriving HD wallet... done" },
  { prefix: "\u2713", text: "Wallet initialized: orama1q7a3b...f29d" },
  { prefix: "", text: "" },
  { prefix: "$", text: "rw balance" },
  { prefix: "\u2192", text: "ORAMA: 3,840.00 $ORAMA (Orama L1)" },
  { prefix: "\u2192", text: "BTC:   0.025 BTC (bridged)" },
  { prefix: "\u2192", text: "ETH:   1.247 ETH" },
  { prefix: "\u2192", text: "SOL:   42.5 SOL" },
  { prefix: "", text: "" },
  { prefix: "$", text: "rw governance vote --proposal 42 --yes" },
  { prefix: "\u2192", text: "Casting vote with NFT governance power..." },
  { prefix: "\u2713", text: "Vote recorded on-chain (Tier 2 proposal)" },
  { prefix: "", text: "" },
  { prefix: "$", text: "rw password store database-prod" },
  { prefix: "\u2192", text: "Splitting secret with Shamir's SSS (K=3, N=5)" },
  { prefix: "\u2192", text: "Distributing to Orama Vault... done" },
  { prefix: "\u2713", text: "Password stored: database-prod" },
  { prefix: "", text: "" },
  { prefix: "$", text: "rw ssh generate production-server" },
  { prefix: "\u2192", text: "Generating Ed25519 key pair... done" },
  { prefix: "\u2192", text: "Storing private key in vault... done" },
  { prefix: "\u2713", text: "SSH key stored: production-server" },
];

const features = [
  {
    icon: <Wallet className="w-5 h-5 text-fg" />,
    title: "Orama L1 Native",
    description:
      "The official wallet of Orama Network from day one. Send $ORAMA, bridge BTC, trade on the native DEX, and interact with WASM smart contracts.",
  },
  {
    icon: <Lock className="w-5 h-5 text-fg" />,
    title: "Password Manager",
    description:
      "Store and retrieve passwords securely. Secrets split via Shamir's SSS and distributed across the Orama Vault.",
  },
  {
    icon: <KeyRound className="w-5 h-5 text-fg" />,
    title: "SSH Key Store",
    description:
      "Generate, store, and manage SSH keys. Connect to servers directly from the CLI.",
  },
  {
    icon: <LinkIcon className="w-5 h-5 text-fg" />,
    title: "Governance & NFTs",
    description:
      "Cast governance votes, receive NFT bridge fee revenue, and manage DeBros NFTs — all from RootWallet.",
  },
  {
    icon: <TerminalIcon className="w-5 h-5 text-fg" />,
    title: "CLI + SDK",
    description:
      "Full command-line interface plus TypeScript SDK with React hooks for building wallet-connected apps.",
  },
  {
    icon: <Layers className="w-5 h-5 text-fg" />,
    title: "Multi-Chain",
    description:
      "Orama L1 (primary), Bitcoin, EVM chains (Ethereum, Base, Polygon), and Solana. One wallet, every chain.",
  },
];

const sdkCode = `import { useWallet } from '@debros/rootwallet-sdk'

function App() {
  const { address, balance, signTransaction } = useWallet({
    chain: 'orama' // Primary: Orama L1
  })

  return (
    <div>
      <p>Connected: {address}</p>
      <p>Balance: {balance} $ORAMA</p>
      <button onClick={() => signTransaction(tx)}>
        Sign Transaction
      </button>
    </div>
  )
}`;

const securityRows = [
  { label: "Encryption", value: "AES-256-GCM" },
  { label: "Key Derivation", value: "BIP-39 + BIP-44" },
  { label: "Secret Splitting", value: "Shamir's Secret Sharing (K=3, N=5)" },
  { label: "Storage", value: "Distributed across Orama Vault nodes" },
  { label: "Signing", value: "Local only — keys never leave your device" },
  { label: "Source", value: "Fully open source" },
];

export default function RootWallet() {
  return (
    <Page title="RootWallet — Self-Custody Wallet & Password Manager">
      {/* Hero */}
      <Section padding="wide">
        <div className="relative min-h-[75vh] flex items-center justify-center overflow-hidden">
          <SlashCanvas />

          {/* Animated slash logo */}
          <div className="relative flex flex-col items-center text-center gap-8 z-10">
            <div className="relative">
              <span className="rw-slash text-[10rem] lg:text-[14rem] font-mono font-bold text-fg leading-none select-none">
                /
              </span>
              <div className="absolute inset-0 rw-slash-glow" />
            </div>

            <div className="flex flex-col items-center gap-3">
              <h1 className="font-display font-bold text-4xl lg:text-5xl text-fg tracking-tight">
                RootWallet
              </h1>
              <p className="font-mono text-base text-muted tracking-wider">
                Your keys. Your passwords. Your control.
              </p>
            </div>

            <div className="flex flex-wrap items-center justify-center gap-2">
              <Badge variant="default">SELF-CUSTODY</Badge>
              <Badge variant="default">MULTI-CHAIN</Badge>
              <Badge variant="default">OPEN SOURCE</Badge>
            </div>

            <div className="flex flex-wrap items-center justify-center gap-3 pt-2">
              <Button asChild size="lg">
                <Link to="/docs">Install CLI</Link>
              </Button>
              <Button asChild variant="ghost" size="lg">
                <a
                  href="https://github.com/debros/rootwallet"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  View on GitHub
                </a>
              </Button>
            </div>
          </div>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* What is RootWallet */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="What is RootWallet"
              subtitle="The official wallet of Orama Network from day one. Wallets, governance, passwords, and SSH keys — backed by decentralized secret storage."
            />

            <div className="grid grid-cols-1 md:grid-cols-3 gap-0">
              <div className="border border-dashed border-border p-6 flex flex-col gap-3">
                <div className="flex items-center gap-3">
                  <Wallet className="w-5 h-5 text-fg shrink-0" />
                  <h3 className="font-display font-semibold text-fg">Crypto Wallet</h3>
                </div>
                <ul className="text-sm text-muted space-y-1.5 pl-8">
                  <li>Orama L1 + Bitcoin (primary chains)</li>
                  <li>EVM chains + Solana (secondary)</li>
                  <li>Native DEX trading, BTC bridge, governance</li>
                </ul>
              </div>

              <div className="border border-dashed border-border p-6 flex flex-col gap-3">
                <div className="flex items-center gap-3">
                  <Lock className="w-5 h-5 text-fg shrink-0" />
                  <h3 className="font-display font-semibold text-fg">Password Manager</h3>
                </div>
                <ul className="text-sm text-muted space-y-1.5 pl-8">
                  <li>Store and retrieve passwords from CLI</li>
                  <li>Secrets split with Shamir's Secret Sharing</li>
                  <li>Distributed across Orama Vault nodes</li>
                </ul>
              </div>

              <div className="border border-dashed border-border p-6 flex flex-col gap-3">
                <div className="flex items-center gap-3">
                  <KeyRound className="w-5 h-5 text-fg shrink-0" />
                  <h3 className="font-display font-semibold text-fg">SSH Key Store</h3>
                </div>
                <ul className="text-sm text-muted space-y-1.5 pl-8">
                  <li>Generate Ed25519 key pairs</li>
                  <li>Store private keys in the vault</li>
                  <li>Connect to servers with one command</li>
                </ul>
              </div>
            </div>

            <div className="flex flex-wrap items-center justify-center gap-6 text-xs font-mono text-muted border border-dashed border-border p-4">
              <div className="flex items-center gap-2">
                <Shield className="w-3.5 h-3.5" />
                <span>Keys never leave your device</span>
              </div>
              <div className="flex items-center gap-2">
                <Globe className="w-3.5 h-3.5" />
                <span>Only encrypted shares stored on network</span>
              </div>
              <div className="flex items-center gap-2">
                <Smartphone className="w-3.5 h-3.5" />
                <span>CLI + SDK + Mobile (coming soon)</span>
              </div>
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Features Grid */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader title="Features" />

            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-0">
              {features.map((feature) => (
                <FeatureCard
                  key={feature.title}
                  icon={feature.icon}
                  title={feature.title}
                  description={feature.description}
                  className="[&_.text-accent]:text-fg"
                />
              ))}
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Terminal Demo */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader title="In Action" />
            <Terminal lines={terminalLines} />
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* SDK Integration */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader title="SDK Integration" />
            <SyntaxCodeBlock
              code={sdkCode}
              language="typescript"
              label="FIG.01 — ROOTWALLET REACT SDK"
            />
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Security Model */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader title="Security Model" />
            <SpecTable rows={securityRows} />
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Mobile App Teaser */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader title="Mobile" />

            <DashedPanel withBackground withCorners>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-12 items-center">
                <div className="flex flex-col gap-5">
                  <Badge variant="status" className="w-fit">
                    COMING SOON
                  </Badge>

                  <h3 className="font-display font-bold text-2xl text-fg">
                    RootWallet Mobile
                  </h3>

                  <p className="text-muted leading-relaxed">
                    Manage your wallet, passwords, and SSH keys from your phone.
                    iOS and Android.
                  </p>

                  <div className="flex flex-wrap items-center gap-4 pt-2">
                    <Button variant="ghost" size="default">
                      Notify Me
                    </Button>
                    <span className="text-xs font-mono text-muted">
                      Expected Q3 2026
                    </span>
                  </div>
                </div>

                {/* Phone silhouette */}
                <div className="flex justify-center">
                  <div className="relative w-48 h-80 border-2 border-border rounded-3xl bg-surface">
                    {/* Notch */}
                    <div className="absolute top-3 left-1/2 -translate-x-1/2 w-20 h-5 bg-bg rounded-full border border-border" />

                    {/* Screen content lines */}
                    <div className="absolute inset-x-6 top-14 bottom-14 flex flex-col gap-3">
                      <div className="h-2 bg-border/60 rounded-full w-3/4" />
                      <div className="h-2 bg-border/40 rounded-full w-1/2" />
                      <div className="h-px border-t border-dashed border-border mt-2 mb-2" />
                      <div className="h-2 bg-border/60 rounded-full w-full" />
                      <div className="h-2 bg-border/40 rounded-full w-2/3" />
                      <div className="h-2 bg-border/30 rounded-full w-4/5" />
                      <div className="h-px border-t border-dashed border-border mt-2 mb-2" />
                      <div className="h-2 bg-border/60 rounded-full w-1/2" />
                      <div className="h-2 bg-border/40 rounded-full w-3/4" />
                      <div className="flex-1" />
                      <div className="h-8 border border-dashed border-border rounded-sm flex items-center justify-center">
                        <span className="text-[10px] font-mono text-muted">
                          /rw
                        </span>
                      </div>
                    </div>

                    {/* Home indicator */}
                    <div className="absolute bottom-2 left-1/2 -translate-x-1/2 w-16 h-1 bg-border rounded-full" />
                  </div>
                </div>
              </div>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* CTA */}
      <Section padding="wide">
        <AnimateIn>
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col items-center text-center gap-6 py-8">
              <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
                Take ownership of your keys
              </h2>
              <p className="text-muted max-w-lg leading-relaxed">
                Self-custody wallet with network-grade security. No third
                parties. No cloud. Just you and the network.
              </p>
              <div className="flex flex-wrap items-center justify-center gap-3">
                <Button asChild variant="ghost" size="lg">
                  <Link to="/docs">Install CLI</Link>
                </Button>
                <Button asChild variant="ghost" size="lg">
                  <a
                    href="https://github.com/debros/rootwallet"
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    View on GitHub
                  </a>
                </Button>
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>
      </Section>
    </Page>
  );
}
