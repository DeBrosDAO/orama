import { Link } from "react-router";
import { ArrowRight } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { Button } from "../components/ui/button";
import { ServiceGrid } from "../components/sections/service-grid";
import { SectionTitle } from "../components/sections/section-title";
import { CtaBand } from "../components/sections/cta-band";
import { ResilienceDiagram } from "../components/visuals/how-diagrams";
import { ROUTES } from "../content/routes";
import { SERVICES } from "../content/services";

const TOOLS = [
  { name: "One command line", line: "Deploy apps, databases and functions." },
  { name: "SDK", line: "TypeScript and Go clients for your app's code." },
  { name: "One API per app", line: "Every service behind a single address." },
];

export default function Platform() {
  return (
    <Page route={ROUTES.platform}>
      <PageHero
        eyebrow="Platform"
        title="Everything an app needs. All live."
        line={`${SERVICES.length} services that do the job of the big clouds' building blocks, built to run on independently owned machines.`}
      />

      <Section padding="narrow">
        <h2 className="sr-only">Services</h2>
        <ServiceGrid />
      </Section>

      <Section>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-12 items-center">
          <div>
            <SectionTitle
              align="left"
              eyebrow="Built to stay up"
              title="One machine fails. Your app doesn't."
              line="Your main database lives on three machines. Lose one, and the other two carry on."
              className="mb-0 sm:mb-0"
            />
          </div>
          <div className="border border-dashed border-border p-6 sm:p-10">
            <ResilienceDiagram />
          </div>
        </div>
      </Section>

      <Section>
        <SectionTitle eyebrow="How you use it" title="Three ways in." />
        <ul className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {TOOLS.map((t, i) => (
            <li key={t.name} className="flex flex-col gap-3 p-6 border border-dashed border-border">
              <span className="font-mono text-xs text-muted">0{i + 1}</span>
              <h3 className="font-display font-semibold text-lg text-fg">{t.name}</h3>
              <p className="text-sm text-muted">{t.line}</p>
            </li>
          ))}
        </ul>
      </Section>

      <CtaBand title="See what people build with it.">
        <Button asChild size="lg">
          <Link to={ROUTES.useCases.path}>Use cases<ArrowRight className="w-3.5 h-3.5 ml-2" /></Link>
        </Button>
        <Button asChild variant="ghost" size="lg">
          <Link to={ROUTES.howItWorks.path}>How it works</Link>
        </Button>
      </CtaBand>
    </Page>
  );
}
