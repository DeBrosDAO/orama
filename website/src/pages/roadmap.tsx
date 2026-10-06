import { Link } from "react-router";
import { ArrowRight, Box, Cpu, Plug, Wifi } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { Button } from "../components/ui/button";
import { StatusBadge } from "../components/ui/status-badge";
import { SectionTitle } from "../components/sections/section-title";
import { CtaBand } from "../components/sections/cta-band";
import { RoadmapDestination, RoadmapTrack } from "../components/visuals/roadmap-track";
import { ROUTES } from "../content/routes";

const ORAMA_ONE_STEPS = [
  { icon: Box, title: "Unbox", line: "A small node computer." },
  { icon: Plug, title: "Plug in", line: "Power and internet." },
  { icon: Wifi, title: "Join", line: "It connects to the network." },
  { icon: Cpu, title: "Serve", line: "It runs part of the cloud." },
];

function OramaOnePreview() {
  return (
    <Section>
      <SectionTitle
        eyebrow="Step 03"
        title="Orama One: the cloud, in a box."
        line="The end goal: hardware anyone can own, so the network is truly run by the public."
      />
      <div className="flex justify-center mb-8">
        <StatusBadge tone="future">Planned</StatusBadge>
      </div>
      <ol className="grid grid-cols-2 md:grid-cols-4 gap-px bg-border border border-dashed border-signal/20">
        {ORAMA_ONE_STEPS.map(({ icon: Icon, title, line }, i) => (
          <li key={title} className="flex flex-col items-center text-center gap-3 p-6 sm:p-8 bg-surface">
            <span className="font-mono text-[10px] text-muted">0{i + 1}</span>
            <Icon size={32} strokeWidth={1.1} className="text-signal/90" />
            <h3 className="font-display font-semibold text-fg">{title}</h3>
            <p className="text-sm text-muted">{line}</p>
          </li>
        ))}
      </ol>
    </Section>
  );
}

export default function Roadmap() {
  return (
    <Page route={ROUTES.roadmap}>
      <PageHero
        eyebrow="Roadmap"
        title="Three steps to a cloud owned by people."
        line="No dates, no hype. Each step ships when it's done."
      />

      <Section padding="narrow">
        <h2 className="sr-only">Milestones</h2>
        <div className="border border-dashed border-border p-6 sm:p-10">
          <RoadmapTrack detailed />
        </div>
        <div className="mt-12">
          <RoadmapDestination />
        </div>
      </Section>

      <OramaOnePreview />

      <CtaBand title="Want to speed it up?" line="Funding turns this roadmap into a timeline.">
        <Button asChild size="lg">
          <Link to={ROUTES.investors.path}>For investors<ArrowRight className="w-3.5 h-3.5 ml-2" /></Link>
        </Button>
        <Button asChild variant="ghost" size="lg">
          <Link to={ROUTES.donate.path}>Donate</Link>
        </Button>
      </CtaBand>
    </Page>
  );
}
