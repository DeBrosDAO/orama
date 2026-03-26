import { Link } from "react-router";
import { ArrowRight, ExternalLink } from "lucide-react";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { DashedPanel } from "../ui/dashed-panel";
import { AnimateIn } from "../ui/animate-in";

const SPONSORS = [
  {
    name: "DeBros",
    tier: "Platinum",
    logo: "/images/debrosnet.png",
    desc: "Core team and founding sponsor of the Orama Network.",
    url: "https://debros.io",
    color: "#5CE0D8",
  },
  {
    name: "ICXCNIKA",
    tier: "Gold",
    logo: "/images/icxcnika.webp",
    desc: "Early supporter and partner driving ecosystem growth.",
    url: "https://icxcnika.io/",
    color: "#FFD700",
  },
];

export function SponsorsShowcase() {
  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Sponsors"
          subtitle="Backed by believers."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-8">
        {SPONSORS.map((sponsor) => (
          <AnimateIn key={sponsor.name}>
            <a
              href={sponsor.url}
              target="_blank"
              rel="noopener noreferrer"
              className="block"
            >
              <DashedPanel
                withCorners
                withBackground
                className="h-full hover:bg-white/[0.02] transition-colors"
              >
                <div className="flex flex-col gap-4 p-4" style={{ borderLeft: `2px solid ${sponsor.color}` }}>
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                      <img
                        src={sponsor.logo}
                        alt={sponsor.name}
                        className="w-10 h-10 object-contain"
                      />
                      <div className="flex items-center gap-2">
                        <h3 className="font-display font-bold text-lg text-fg">
                          {sponsor.name}
                        </h3>
                        <ExternalLink className="w-3 h-3 text-muted" />
                      </div>
                    </div>
                    <span
                      className="px-2.5 py-0.5 text-[10px] font-mono font-bold tracking-widest uppercase rounded-full"
                      style={{
                        background: `${sponsor.color}20`,
                        color: sponsor.color,
                        border: `1px solid ${sponsor.color}50`,
                      }}
                    >
                      {sponsor.tier}
                    </span>
                  </div>
                  <p className="text-sm text-muted leading-relaxed">
                    {sponsor.desc}
                  </p>
                </div>
              </DashedPanel>
            </a>
          </AnimateIn>
        ))}
      </div>

      <AnimateIn>
        <div className="flex justify-center mt-6">
          <Link
            to="/invest?tab=sponsors"
            className="flex items-center gap-2 text-xs font-mono text-muted hover:text-fg transition-colors tracking-wider uppercase"
          >
            Become a Sponsor
            <ArrowRight className="w-3.5 h-3.5" />
          </Link>
        </div>
      </AnimateIn>
    </Section>
  );
}
