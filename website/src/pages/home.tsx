import { Link } from "react-router";
import { ArrowRight } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { Button } from "../components/ui/button";
import { AnimateIn } from "../components/ui/animate-in";
import { FactStrip } from "../components/ui/fact-strip";
import { HeroMesh } from "../components/landing/hero-mesh";
import { SectionTitle } from "../components/sections/section-title";
import { ServiceGrid } from "../components/sections/service-grid";
import { UseCaseGrid } from "../components/sections/use-case-grid";
import { CtaBand } from "../components/sections/cta-band";
import { CloudCompare } from "../components/visuals/cloud-compare";
import { ClusterDiagram, MeshDiagram, WalletLoginDiagram } from "../components/visuals/how-diagrams";
import { RoadmapTrack } from "../components/visuals/roadmap-track";
import { APPS } from "../content/apps";
import { ROUTES } from "../content/routes";
import { GITHUB_URL } from "../content/site";
import { PILLARS } from "../content/why";

function Hero() {
  return (
    <section className="relative -mt-16 min-h-[100svh] flex items-center justify-center overflow-hidden">
      <HeroMesh />
      <div className="relative z-10 flex flex-col items-center text-center gap-7 max-w-3xl mx-auto px-4 sm:px-6 pt-28 pb-20">
        <span className="inline-flex items-center gap-2 px-3 py-1 text-[11px] font-mono tracking-widest uppercase rounded-full border border-dashed border-border text-muted">
          <span className="w-1.5 h-1.5 rounded-full bg-signal" />
          Early · working proof of concept
        </span>
        <h1 className="font-display font-bold text-[2.5rem] leading-[1.04] sm:text-6xl lg:text-7xl tracking-tight text-fg text-balance">
          The cloud,
          <br />
          owned by no one.
        </h1>
        <p className="text-muted text-lg sm:text-xl max-w-xl text-pretty">
          Everything an app needs to run, built for machines run by independent people, not one giant company.
        </p>
        <div className="flex flex-col sm:flex-row w-full sm:w-auto gap-3 justify-center pt-2">
          <Button asChild size="lg">
            <Link to={ROUTES.howItWorks.path}>
              See how it works
              <ArrowRight className="w-3.5 h-3.5 ml-2" />
            </Link>
          </Button>
          <Button asChild variant="ghost" size="lg">
            <Link to={ROUTES.investors.path}>Back the network</Link>
          </Button>
        </div>
      </div>
    </section>
  );
}

const STEPS = [
  { n: "01", title: "Machines join", line: "Independent computers link up over encrypted connections.", Visual: MeshDiagram },
  { n: "02", title: "Your app gets a cluster", line: "Its own database, cache and gateway on three of them, not shared tables.", Visual: ClusterDiagram },
  { n: "03", title: "Sign in with a wallet", line: "No email, no password, no account to leak.", Visual: WalletLoginDiagram },
];

function HowSteps() {
  return (
    <Section>
      <SectionTitle eyebrow="How it works" title="Three steps. No single owner." />
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        {STEPS.map(({ n, title, line, Visual }) => (
          <AnimateIn key={n} className="flex flex-col gap-4 p-6 border border-dashed border-border">
            <span className="font-mono text-xs text-muted">{n}</span>
            <div className="aspect-[40/27] flex items-center"><Visual /></div>
            <h3 className="font-display font-semibold text-lg text-fg">{title}</h3>
            <p className="text-sm text-muted">{line}</p>
          </AnimateIn>
        ))}
      </div>
      <MoreLink to={ROUTES.howItWorks.path}>How it works</MoreLink>
    </Section>
  );
}

function MoreLink({ to, children }: { to: string; children: string }) {
  return (
    <div className="flex justify-center mt-10">
      <Link to={to} className="group inline-flex items-center gap-2 font-mono text-xs tracking-widest uppercase text-muted hover:text-fg transition-colors">
        {children}
        <ArrowRight size={14} className="transition-transform group-hover:translate-x-1" />
      </Link>
    </div>
  );
}

function AppsTeaser() {
  return (
    <Section>
      <SectionTitle eyebrow="Proof" title="Already working today." />
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4">
        {APPS.map((app) => (
          <Link
            key={app.id}
            to={`${ROUTES.apps.path}#${app.id}`}
            className="group flex items-center gap-5 p-6 border border-dashed border-border hover:border-fg/25 hover:bg-white/[0.02] transition-colors"
          >
            <span className="flex items-center justify-center w-16 h-16 shrink-0 rounded-2xl border border-border bg-surface-2">
              <img src={app.mark} alt="" className="w-9 h-9 object-contain" loading="lazy" />
            </span>
            <span className="flex flex-col gap-1 min-w-0">
              <span className="font-display font-bold text-xl text-fg">{app.name}</span>
              <span className="text-sm text-muted">{app.tagline}</span>
              <span className="font-mono text-[10px] tracking-wider uppercase text-accent">{app.status}</span>
            </span>
            <ArrowRight size={16} className="ml-auto shrink-0 text-muted group-hover:text-fg group-hover:translate-x-1 transition-all" />
          </Link>
        ))}
      </div>
      <FactStrip />
    </Section>
  );
}

function WhyItMatters() {
  return (
    <Section>
      <SectionTitle eyebrow="Why it matters" title="The internet shouldn't have an owner." />
      <ul className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-px bg-border border border-border">
        {PILLARS.map(({ icon: Icon, title, line }) => (
          <li key={title} className="flex sm:flex-col items-start gap-4 sm:gap-3 p-6 sm:p-8 bg-surface">
            <Icon size={30} strokeWidth={1.25} className="text-fg shrink-0" />
            <div className="flex flex-col gap-1.5 sm:gap-3">
              <h3 className="font-display font-semibold text-fg">{title}</h3>
              <p className="text-sm text-muted">{line}</p>
            </div>
          </li>
        ))}
      </ul>
    </Section>
  );
}

export default function Home() {
  return (
    <Page route={ROUTES.home}>
      <Hero />

      <Section>
        <SectionTitle eyebrow="The problem" title="Today, the internet has an off switch." />
        <CloudCompare />
      </Section>

      <Section>
        <SectionTitle eyebrow="What you get" title="Everything an app needs." />
        <ServiceGrid compact />
        <MoreLink to={ROUTES.platform.path}>Explore the platform</MoreLink>
      </Section>

      <HowSteps />
      <AppsTeaser />

      <Section>
        <SectionTitle eyebrow="What it's for" title="Built for people, not platforms." />
        <UseCaseGrid ids={["messaging", "speak-freely", "backups", "degoogled", "satellite", "disaster"]} />
        <MoreLink to={ROUTES.useCases.path}>All use cases</MoreLink>
      </Section>

      <WhyItMatters />

      <Section>
        <SectionTitle eyebrow="Roadmap" title="Where we're going." />
        <RoadmapTrack />
        <MoreLink to={ROUTES.roadmap.path}>Full roadmap</MoreLink>
      </Section>

      <CtaBand title="Help build the people's cloud." line="Orama is open source. Back it, fund it, or read the code.">
        <Button asChild size="lg">
          <Link to={ROUTES.investors.path}>Investors<ArrowRight className="w-3.5 h-3.5 ml-2" /></Link>
        </Button>
        <Button asChild variant="ghost" size="lg">
          <Link to={ROUTES.donate.path}>Donate</Link>
        </Button>
        <Button asChild variant="ghost" size="lg">
          <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer">GitHub</a>
        </Button>
      </CtaBand>
    </Page>
  );
}
