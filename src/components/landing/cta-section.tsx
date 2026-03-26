import { Link } from "react-router";
import { ExternalLink } from "lucide-react";
import type { ReactNode } from "react";
import type { Persona } from "../../types/persona";
import { Section } from "../layout/section";
import { DashedPanel } from "../ui/dashed-panel";
import { Button } from "../ui/button";
import { AnimateIn } from "../ui/animate-in";
import { StatusDot } from "../ui/status-dot";
import { Redacted } from "../ui/redacted";

const ctaContent: Record<
  Persona,
  {
    heading: string;
    description: ReactNode;
    buttonText: string;
    to: string;
    external?: boolean;
  }
> = {
  developer: {
    heading: "Start building in 60 seconds.",
    description:
      "Free tier. No credit card. No email. Connect your wallet and deploy.",
    buttonText: "Start Building",
    to: "/dashboard",
  },
  operator: {
    heading: "Start your node today.",
    description:
      <>Minimal hardware. Maximum rewards. Join <Redacted /> operators powering the decentralized cloud.</>,
    buttonText: "Read Setup Guide",
    to: "/docs/operator/getting-started",
  },
  contributor: {
    heading: "Your first PR is waiting.",
    description:
      "Open source. Active development. Real impact. Pick an issue and start contributing.",
    buttonText: "View on GitHub",
    to: "https://github.com/DeBrosOfficial",
    external: true,
  },
};

export function CtaSection({ persona }: { persona: Persona }) {
  const content = ctaContent[persona];

  return (
    <Section padding="wide">
      <AnimateIn>
      <DashedPanel withCorners withBackground>
        <div className="flex flex-col items-center text-center gap-6 py-8">
          <div className="flex items-center justify-center gap-2 mb-2">
            <StatusDot status="active" />
            <span className="text-xs font-mono text-muted tracking-wider uppercase">50+ Nodes Online</span>
          </div>

          <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
            {content.heading}
          </h2>

          <p className="text-muted max-w-lg leading-relaxed">
            {content.description}
          </p>

          {content.external ? (
            <Button asChild size="lg">
              <a
                href={content.to}
                target="_blank"
                rel="noopener noreferrer"
              >
                {content.buttonText}
                <ExternalLink className="w-3.5 h-3.5 ml-2" />
              </a>
            </Button>
          ) : (
            <Button asChild size="lg">
              <Link to={content.to}>{content.buttonText}</Link>
            </Button>
          )}
        </div>
      </DashedPanel>
      </AnimateIn>
    </Section>
  );
}
