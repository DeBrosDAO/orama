import { Link } from "react-router";
import { Download, ExternalLink, ArrowRight } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";
import { SplitText } from "../components/ui/split-text";
import { SILVER } from "../components/ui/silver-theme";

const VERSIONS = [
  {
    version: "v3.0",
    title: "The Eternal Decentralized Computer and Financial System",
    subtitle: "Standalone Layer-1 Blockchain — 1,000-Year Horizon",
    date: "March 2026",
    current: true,
    file: "/orama-whitepaper-v3.pdf",
    highlights: [
      "Standalone L1 blockchain with pure WASM smart contracts",
      "210M $ORAMA hard cap — zero pre-mine, 100% mined",
      "BTC-only economy with native trust-minimized bridge",
      "PLONK zk-SNARKs for per-transaction public/private toggle",
      "Hybrid PoS + Proof of Contribution + Proof of Infrastructure consensus",
      "AI Marketplace with autonomous Angels (AI agents on-chain)",
      "OramaOS — hardened node OS with TPM attestation and 1.5x multiplier",
      "300-node genesis requirement — mainnet when verified",
    ],
  },
  {
    version: "v2.0",
    title: "Infrastructure Whitepaper",
    subtitle: "Decentralized Cloud Infrastructure & Governance",
    date: "2025",
    current: false,
    file: "/orama-whitepaper-v2.pdf",
    highlights: [
      "On-chain governance model",
      "Refined tokenomics",
      "Security features & architecture",
      "Strategic roadmap",
    ],
  },
  {
    version: "v1.0",
    title: "Genesis Whitepaper",
    subtitle: "Original Vision & Core Principles",
    date: "2024",
    current: false,
    file: "/orama-whitepaper-v1.pdf",
    highlights: [
      "Initial vision & core principles",
      "Early tokenomics",
      "Technical architecture foundations",
      "Ecosystem overview",
    ],
  },
];

export default function Whitepaper() {
  return (
    <Page title="Whitepaper — Orama Network">
      {/* Hero */}
      <Section padding="wide">
        <div className="flex flex-col items-center text-center min-h-[40vh] pt-[12vh] gap-6 max-w-3xl mx-auto">
          <Badge variant="default" className="w-fit">WHITEPAPER</Badge>

          <h1 className="font-display font-bold text-4xl lg:text-6xl leading-tight">
            <SplitText
              text="Read the vision."
              className="text-fg"
              delay={30}
              duration={0.6}
              splitType="chars"
              from={{ opacity: 0, y: 30 }}
              to={{ opacity: 1, y: 0 }}
            />
          </h1>

          <p className="text-muted text-sm leading-relaxed max-w-lg">
            Comprehensive technical documentation covering the Orama Network's
            architecture, consensus mechanism, tokenomics, and roadmap.
          </p>

          <Button asChild size="lg">
            <a href={VERSIONS[0].file} target="_blank" rel="noopener noreferrer">
              <Download className="w-4 h-4 mr-2" />
              Download Latest (v3.0)
            </a>
          </Button>
        </div>
      </Section>

      <Section padding="none"><CrosshairDivider /></Section>

      {/* Versions */}
      <Section>
        <AnimateIn>
          <SectionHeader
            title="All Versions"
            subtitle="Track the evolution of Orama Network through its whitepapers."
          />
        </AnimateIn>

        <div className="flex flex-col gap-6 mt-8">
          {VERSIONS.map((v) => (
            <AnimateIn key={v.version}>
              <DashedPanel
                withCorners
                withBackground
                className={v.current ? "border-accent/30" : ""}
              >
                <div className="flex flex-col md:flex-row md:items-start gap-6 p-4 sm:p-6">
                  {/* Version badge */}
                  <div className="flex flex-col items-center gap-2 md:w-32 shrink-0">
                    <span
                      className="font-display font-bold text-2xl"
                      style={v.current ? {
                        background: SILVER.gradient,
                        WebkitBackgroundClip: "text",
                        WebkitTextFillColor: "transparent",
                      } : { color: SILVER.mid }}
                    >
                      {v.version}
                    </span>
                    {v.current && (
                      <Badge variant="accent" className="text-[10px]">CURRENT</Badge>
                    )}
                    <span className="text-[10px] font-mono text-muted">{v.date}</span>
                  </div>

                  {/* Content */}
                  <div className="flex-1 flex flex-col gap-3">
                    <h3 className="font-display font-bold text-lg text-fg">{v.title}</h3>
                    <p className="text-sm text-muted">{v.subtitle}</p>

                    <ul className="flex flex-col gap-1.5 mt-2">
                      {v.highlights.map((h) => (
                        <li key={h} className="flex items-start gap-2">
                          <span className="w-1 h-1 rounded-full bg-accent/50 mt-2 shrink-0" />
                          <span className="text-xs text-muted">{h}</span>
                        </li>
                      ))}
                    </ul>
                  </div>

                  {/* Actions */}
                  <div className="flex flex-col gap-2 md:w-48 shrink-0">
                    <Button asChild size="default" className={v.current ? "" : "opacity-70"}>
                      <a href={v.file} target="_blank" rel="noopener noreferrer">
                        <ExternalLink className="w-3.5 h-3.5 mr-2" />
                        Read Online
                      </a>
                    </Button>
                    <Button asChild variant="ghost" size="default">
                      <a href={v.file} download>
                        <Download className="w-3.5 h-3.5 mr-2" />
                        Download PDF
                      </a>
                    </Button>
                  </div>
                </div>
              </DashedPanel>
            </AnimateIn>
          ))}
        </div>
      </Section>

      <Section padding="none"><CrosshairDivider /></Section>

      {/* CTA */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col items-center text-center gap-6">
            <h2 className="font-display font-bold text-2xl text-fg">
              Ready to invest?
            </h2>
            <p className="text-muted max-w-md text-sm">
              Now that you've read the vision, join the network as an investor
              or developer.
            </p>
            <div className="flex flex-col sm:flex-row gap-3">
              <Button asChild size="lg">
                <Link to="/investors">
                  Become an Investor
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </Link>
              </Button>
              <Button asChild variant="ghost" size="lg">
                <a href="https://t.me/debrosportal" target="_blank" rel="noopener noreferrer">
                  Join the Waitlist
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </a>
              </Button>
            </div>
          </div>
        </AnimateIn>
      </Section>
    </Page>
  );
}
