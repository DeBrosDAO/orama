import { Link } from "react-router";
import { ArrowRight } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { Button } from "../components/ui/button";
import { SectionTitle } from "../components/sections/section-title";
import { UseCaseGrid, UseCaseLegend } from "../components/sections/use-case-grid";
import { CtaBand } from "../components/sections/cta-band";
import { ROUTES } from "../content/routes";
import { PILLARS } from "../content/why";

export default function UseCases() {
  return (
    <Page route={ROUTES.useCases}>
      <PageHero
        eyebrow="Use cases"
        title={<>What a <span className="whitespace-nowrap">people-owned</span> cloud makes possible.</>}
        line="Some of these run today. Some are ready to build. Some arrive with Orama One."
      >
        <div className="pt-4">
          <UseCaseLegend />
        </div>
      </PageHero>

      <Section padding="narrow">
        <h2 className="sr-only">Use cases</h2>
        <UseCaseGrid showServices invite />
      </Section>

      <Section>
        <SectionTitle eyebrow="Why it matters" title="Infrastructure is power. It should be shared." />
        <ul className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          {PILLARS.map(({ icon: Icon, title, line }) => (
            <li key={title} className="flex items-start gap-5 p-6 sm:p-8 border border-dashed border-border">
              <Icon size={36} strokeWidth={1.1} className="text-fg shrink-0" />
              <div className="flex flex-col gap-2">
                <h3 className="font-display font-semibold text-lg text-fg">{title}</h3>
                <p className="text-sm text-muted">{line}</p>
              </div>
            </li>
          ))}
        </ul>
      </Section>

      <CtaBand title="See it working today." line="AnChat runs on Orama. RootWallet signs you in.">
        <Button asChild size="lg">
          <Link to={ROUTES.apps.path}>See the apps<ArrowRight className="w-3.5 h-3.5 ml-2" /></Link>
        </Button>
      </CtaBand>
    </Page>
  );
}
