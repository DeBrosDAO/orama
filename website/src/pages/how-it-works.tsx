import type { ComponentType } from "react";
import { Link } from "react-router";
import { ArrowRight } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { Button } from "../components/ui/button";
import { AnimateIn } from "../components/ui/animate-in";
import { CtaBand } from "../components/sections/cta-band";
import {
  ClusterDiagram,
  MeshDiagram,
  RequestFlowDiagram,
  ResilienceDiagram,
  WalletLoginDiagram,
} from "../components/visuals/how-diagrams";
import { ROUTES } from "../content/routes";
import { cn } from "../lib/utils";

interface Step {
  n: string;
  title: string;
  line: string;
  Visual: ComponentType;
}

const STEPS: Step[] = [
  {
    n: "01",
    title: "Independent machines join one network.",
    line: "Operators add machines by invitation. Every connection between them is encrypted.",
    Visual: MeshDiagram,
  },
  {
    n: "02",
    title: "Your app gets its own private cluster.",
    line: "Three machines run your own database, cache and gateway, not tables shared with other apps.",
    Visual: ClusterDiagram,
  },
  {
    n: "03",
    title: "Visitors reach it through any machine.",
    line: "Orama runs its own naming and certificates, so your app's address and HTTPS just work.",
    Visual: RequestFlowDiagram,
  },
  {
    n: "04",
    title: "You sign in with a wallet.",
    line: "You prove who you are by signing a message. No email, no password to steal.",
    Visual: WalletLoginDiagram,
  },
  {
    n: "05",
    title: "A machine fails. Your app keeps running.",
    line: "Your data is kept in sync on all three, so one going down doesn't take your app with it.",
    Visual: ResilienceDiagram,
  },
];

export default function HowItWorks() {
  return (
    <Page route={ROUTES.howItWorks}>
      <PageHero
        eyebrow="How it works"
        title="Many machines. One cloud."
        line="Orama explained in five pictures."
      />

      {STEPS.map(({ n, title, line, Visual }, i) => (
        <Section key={n} padding="narrow">
          <AnimateIn>
            <div
              className={cn(
                "grid grid-cols-1 lg:grid-cols-2 gap-8 lg:gap-16 items-center",
                i > 0 && "border-t border-dashed border-border pt-10 sm:pt-14",
              )}
            >
              <div className={cn("flex flex-col gap-4", i % 2 === 1 && "lg:order-2")}>
                <span aria-hidden="true" className="font-mono text-5xl sm:text-6xl font-bold text-border">{n}</span>
                <h2 className="font-display font-bold text-2xl sm:text-3xl text-fg tracking-tight text-balance">{title}</h2>
                <p className="text-muted max-w-md">{line}</p>
              </div>
              <div className={cn("border border-dashed border-border p-3 sm:p-10 bg-surface/50", i % 2 === 1 && "lg:order-1")}>
                <Visual />
              </div>
            </div>
          </AnimateIn>
        </Section>
      ))}

      <CtaBand title="Now picture what you'd build on it.">
        <Button asChild size="lg">
          <Link to={ROUTES.useCases.path}>Use cases<ArrowRight className="w-3.5 h-3.5 ml-2" /></Link>
        </Button>
        <Button asChild variant="ghost" size="lg">
          <Link to={ROUTES.whitepaper.path}>Read the whitepaper</Link>
        </Button>
      </CtaBand>
    </Page>
  );
}
