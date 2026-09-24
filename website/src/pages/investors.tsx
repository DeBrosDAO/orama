import { Link } from "react-router";
import { ArrowRight, Building, Coins, Mail, TrendingUp } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { Button } from "../components/ui/button";
import { FactStrip } from "../components/ui/fact-strip";
import { SectionTitle } from "../components/sections/section-title";
import { CtaBand } from "../components/sections/cta-band";
import { FundingDonut } from "../components/visuals/funding-donut";
import { CloudCompare } from "../components/visuals/cloud-compare";
import { FUNDING_MONTHS, FUNDING_TOTAL_EUR, REVENUE_MONTH, TIMELINE, formatEur } from "../content/funding";
import { ROUTES } from "../content/routes";
import { INVESTOR_EMAIL } from "../content/site";
import { cn } from "../lib/utils";

const INVESTOR_MAILTO = `mailto:${INVESTOR_EMAIL}?subject=${encodeURIComponent("Investing in Orama")}`;

const MODEL = [
  { icon: Coins, title: "Usage-based", line: "Apps pay for what they use. No flat fees." },
  { icon: TrendingUp, title: `Revenue from month ${REVENUE_MONTH}`, line: "Once Orama One nodes are in the public's hands." },
  { icon: Building, title: "Swiss company", line: "Formed when the round closes." },
];

function Timeline() {
  return (
    <div className="relative">
      <div
        aria-hidden="true"
        className="absolute left-[7px] top-2 bottom-2 border-l md:left-0 md:right-0 md:top-[7px] md:bottom-auto md:border-l-0 md:border-t border-dashed border-border"
      />
      <ol className="grid grid-cols-1 md:grid-cols-4 gap-8 md:gap-4">
        {TIMELINE.map((stop, i) => {
          const last = i === TIMELINE.length - 1;
          return (
            <li key={stop.month} className="relative flex md:flex-col gap-4">
              <span className={cn("relative z-10 mt-0.5 w-3.5 h-3.5 shrink-0 rounded-full border", last ? "bg-fg border-fg" : "bg-bg border-fg/50")} />
              <div className="flex flex-col gap-1">
                <span className="font-mono text-xs tracking-widest uppercase text-muted">Month {stop.month}</span>
                <span className="font-display font-semibold text-fg text-lg">{stop.title}</span>
                <span className="text-sm text-muted">{stop.line}</span>
              </div>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

export default function Investors() {
  return (
    <Page route={ROUTES.investors}>
      <PageHero
        eyebrow="Investors"
        title="Back the people's cloud."
        line={`${formatEur(FUNDING_TOTAL_EUR)} to take Orama from a working prototype to a network anyone can join.`}
      >
        <div className="pt-4">
          <Button asChild size="lg">
            <a href={INVESTOR_MAILTO}>
              <Mail className="w-3.5 h-3.5 mr-2" />
              {INVESTOR_EMAIL}
            </a>
          </Button>
        </div>
      </PageHero>

      <Section padding="narrow">
        <SectionTitle eyebrow="The opportunity" title="A few companies own the cloud. Orama gives it back." />
        <CloudCompare />
      </Section>

      <Section>
        <SectionTitle eyebrow="Already built" title="This isn't an idea. It runs." />
        <FactStrip />
      </Section>

      <Section>
        <SectionTitle eyebrow="Use of funds" title={`Where ${formatEur(FUNDING_TOTAL_EUR)} goes.`} line={`${FUNDING_MONTHS} months, three roadmap steps.`} />
        <FundingDonut />
      </Section>

      <Section>
        <SectionTitle eyebrow="Timeline" title="From funding to revenue." />
        <div className="border border-dashed border-border p-6 sm:p-10">
          <Timeline />
        </div>
      </Section>

      <Section>
        <SectionTitle eyebrow="The model" title="Simple by design." />
        <ul className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {MODEL.map(({ icon: Icon, title, line }) => (
            <li key={title} className="flex flex-col gap-3 p-6 border border-dashed border-border">
              <Icon size={26} strokeWidth={1.25} className="text-fg" />
              <h3 className="font-display font-semibold text-lg text-fg">{title}</h3>
              <p className="text-sm text-muted">{line}</p>
            </li>
          ))}
        </ul>
      </Section>

      <CtaBand title="Let's talk." line="Tell us who you are and what you'd like to know.">
        <Button asChild size="lg">
          <a href={INVESTOR_MAILTO}>
            {INVESTOR_EMAIL}
            <ArrowRight className="w-3.5 h-3.5 ml-2" />
          </a>
        </Button>
        <Button asChild variant="ghost" size="lg">
          <Link to={ROUTES.whitepaper.path}>Read the whitepaper</Link>
        </Button>
      </CtaBand>
    </Page>
  );
}
