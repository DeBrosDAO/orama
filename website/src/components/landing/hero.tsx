import { Link } from "react-router";
import type { Persona } from "../../types/persona";
import { Section } from "../layout/section";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { ExternalLink } from "lucide-react";

const heroContent: Record<
  Persona,
  {
    label: string;
    titleLine1: string;
    titleLine2: string;
    description: string;
    primaryCta: { text: string; to: string; external?: boolean };
    secondaryCta: { text: string; to: string };
    comingSoon?: boolean;
    badges: string[];
  }
> = {
  developer: {
    label: "",
    titleLine1: "Ship faster. Pay less.",
    titleLine2: "Own everything.",
    description:
      "Deploy your React app, API, or database in one command. No AWS console. No YAML. No surprise bills. Just push and ship.",
    primaryCta: { text: "Start Building", to: "/dashboard" },
    secondaryCta: { text: "See Documentation", to: "/docs" },
    badges: ["Free Tier (No Credit Card)", "Zero Telemetry", "Decentralized Infrastructure"],
  },
  operator: {
    label: "",
    titleLine1: "Earn by powering",
    titleLine2: "the decentralized cloud.",
    description:
      "Run an Orama node on any VPS. Earn $ORAMA tokens for every request you serve. Join the infrastructure that replaces AWS.",
    primaryCta: { text: "Become an Operator", to: "/dashboard" },
    secondaryCta: { text: "See Documentation", to: "/docs" },
    comingSoon: true,
    badges: ["$ORAMA Rewards", "Deploy on Any VPS", "100+ Operators"],
  },
  contributor: {
    label: "",
    titleLine1: "Become a Contributor",
    titleLine2: "on Orama Network.",
    description:
      "Help build the decentralized cloud. From the gateway to the CLI, every service is open source and needs sharp engineers.",
    primaryCta: {
      text: "View on GitHub",
      to: "https://github.com/DeBrosOfficial",
      external: true,
    },
    secondaryCta: { text: "See Documentation", to: "/docs" },
    badges: ["Open Source", "Go + TypeScript", "Active Community"],
  },
};

function HeroCrosshairGrid() {
  return (
    <div className="absolute inset-0 overflow-hidden pointer-events-none" aria-hidden="true">
      <svg
        className="absolute inset-0 w-full h-full opacity-[0.04]"
        xmlns="http://www.w3.org/2000/svg"
      >
        {/* Vertical center line */}
        <line
          x1="50%" y1="0" x2="50%" y2="100%"
          stroke="currentColor"
          strokeWidth="1"
          strokeDasharray="1000"
          className="text-fg animate-draw-line"
          style={{ strokeDashoffset: 1000 }}
        />
        {/* Horizontal center line */}
        <line
          x1="0" y1="50%" x2="100%" y2="50%"
          stroke="currentColor"
          strokeWidth="1"
          strokeDasharray="1000"
          className="text-fg animate-draw-line"
          style={{ animationDelay: "0.3s", strokeDashoffset: 1000 }}
        />
        {/* Center crosshair circle */}
        <circle
          cx="50%" cy="50%" r="40"
          fill="none"
          stroke="currentColor"
          strokeWidth="0.5"
          className="text-accent animate-crosshair-fade"
          style={{ animationDelay: "0.8s", opacity: 0, animationFillMode: "forwards" }}
        />
        {/* Outer ring */}
        <circle
          cx="50%" cy="50%" r="120"
          fill="none"
          stroke="currentColor"
          strokeWidth="0.5"
          strokeDasharray="6 6"
          className="text-accent/50 animate-crosshair-fade"
          style={{ animationDelay: "1.2s", opacity: 0, animationFillMode: "forwards" }}
        />
      </svg>

      {/* Corner crosshairs */}
      <div className="absolute top-[15%] left-[15%] w-8 h-8 animate-crosshair-fade" style={{ animationDelay: "1.5s", opacity: 0, animationFillMode: "forwards" }}>
        <div className="absolute top-1/2 left-0 w-full h-px bg-accent/20" />
        <div className="absolute left-1/2 top-0 h-full w-px bg-accent/20" />
      </div>
      <div className="absolute top-[15%] right-[15%] w-8 h-8 animate-crosshair-fade" style={{ animationDelay: "1.7s", opacity: 0, animationFillMode: "forwards" }}>
        <div className="absolute top-1/2 left-0 w-full h-px bg-accent/20" />
        <div className="absolute left-1/2 top-0 h-full w-px bg-accent/20" />
      </div>
      <div className="absolute bottom-[15%] left-[15%] w-8 h-8 animate-crosshair-fade" style={{ animationDelay: "1.9s", opacity: 0, animationFillMode: "forwards" }}>
        <div className="absolute top-1/2 left-0 w-full h-px bg-accent/20" />
        <div className="absolute left-1/2 top-0 h-full w-px bg-accent/20" />
      </div>
      <div className="absolute bottom-[15%] right-[15%] w-8 h-8 animate-crosshair-fade" style={{ animationDelay: "2.1s", opacity: 0, animationFillMode: "forwards" }}>
        <div className="absolute top-1/2 left-0 w-full h-px bg-accent/20" />
        <div className="absolute left-1/2 top-0 h-full w-px bg-accent/20" />
      </div>
    </div>
  );
}

export function LandingHero({ persona }: { persona: Persona }) {
  const content = heroContent[persona];

  return (
    <>
      <Section padding="wide">
        <div className="relative flex flex-col items-center text-center min-h-[70vh] pt-[12vh] gap-6 max-w-3xl mx-auto">
          <HeroCrosshairGrid />
          <Badge variant="default" className="w-fit">
            {content.label}
          </Badge>

          {content.comingSoon && (
            <Badge variant="status" className="w-fit">
              COMING SOON
            </Badge>
          )}

          <h1 className="font-display font-bold text-4xl lg:text-5xl leading-tight text-fg">
            {content.titleLine1}
            <br />
            <span className="text-accent">{content.titleLine2}</span>
          </h1>

          <div className="flex flex-wrap gap-2 justify-center">
            {content.badges.map((badge) => (
              <Badge key={badge} variant="outline">
                {badge}
              </Badge>
            ))}
          </div>

          <p className="text-muted text-lg leading-relaxed max-w-xl">
            {content.description}
          </p>

          <div className="flex flex-wrap items-center gap-3 justify-center pt-2">
            {content.primaryCta.external ? (
              <Button asChild size="lg">
                <a
                  href={content.primaryCta.to}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {content.primaryCta.text}
                  <ExternalLink className="w-3.5 h-3.5 ml-2" />
                </a>
              </Button>
            ) : (
              <Button asChild size="lg">
                <Link to={content.primaryCta.to}>
                  {content.primaryCta.text}
                </Link>
              </Button>
            )}

            {content.secondaryCta.to.startsWith("#") ? (
              <Button asChild variant="ghost" size="lg">
                <a href={content.secondaryCta.to}>
                  {content.secondaryCta.text}
                </a>
              </Button>
            ) : (
              <Button asChild variant="ghost" size="lg">
                <Link to={content.secondaryCta.to}>
                  {content.secondaryCta.text}
                </Link>
              </Button>
            )}
          </div>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>
    </>
  );
}
