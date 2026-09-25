import { Link } from "react-router";
import { ArrowRight, Download, Mail, Plus } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { Button } from "../components/ui/button";
import { StatusBadge } from "../components/ui/status-badge";
import { SectionTitle } from "../components/sections/section-title";
import { CtaBand } from "../components/sections/cta-band";
import { FundingDonut } from "../components/visuals/funding-donut";
import { CardList, CompetitionGrid, SourcesList, StatGrid } from "../components/investors/blocks";
import { Flywheel, MarketLayers, OutageList, ProductPair, ProofPoints, RoundTimeline, SeeForYourself } from "../components/investors/sections";
import { OramaRevenue, Projections, WalletRevenue } from "../components/investors/revenue";
import { RoundTerms, Shipped, ThirtySeconds } from "../components/investors/summary";
import { SourceRef } from "../components/investors/source-ref";
import { FUNDING_MONTHS, FUNDING_TOTAL_EUR, formatEur, formatEurShort } from "../content/funding";
import { CLOUD_STATS, SOVEREIGNTY_STATS, WALLET_STATS } from "../content/investors/market";
import { CLOUD_COMPETITION, COMPARABLES, WALLET_COMPETITION } from "../content/investors/competition";
import { GRANTS, HARDWARE } from "../content/investors/model";
import { DISCLAIMER, FAQ, MOAT, RISKS } from "../content/investors/case";
import { ROUTES } from "../content/routes";
import { CONTACT_EMAIL, INVESTOR_PDF } from "../content/site";

const INVESTOR_MAILTO = `mailto:${CONTACT_EMAIL}?subject=${encodeURIComponent("Investing in Orama Network")}`;

/** The same page as a PDF, to save or forward. Not shown on the printout itself. */
function DownloadPdf() {
  return (
    <Button asChild variant="ghost" size="lg" className="rounded-full no-print">
      <a href={INVESTOR_PDF.path} download>
        <Download className="w-3.5 h-3.5 mr-2" />
        Download as PDF
      </a>
    </Button>
  );
}

const TERMS = [`${formatEurShort(FUNDING_TOTAL_EUR)} equity`, "No token", "Swiss AG, Zug", `${FUNDING_MONTHS} months runway`];

function Hero() {
  return (
    <PageHero
      eyebrow="Investors"
      title="One company. Two products."
      line={`${formatEur(FUNDING_TOTAL_EUR)} to take Orama Network and RootWallet from working products to paying customers.`}
    >
      <ul className="flex flex-wrap justify-center gap-2 pt-2">
        {TERMS.map((t) => (
          <li key={t} className="px-3 py-1 rounded-full border border-dashed border-border font-mono text-[11px] tracking-wider uppercase text-accent">
            {t}
          </li>
        ))}
      </ul>
      <div className="flex flex-col sm:flex-row gap-3 justify-center pt-4">
        <Button asChild size="lg" className="rounded-full">
          <a href={INVESTOR_MAILTO}>
            <Mail className="w-3.5 h-3.5 mr-2" />
            {CONTACT_EMAIL}
          </a>
        </Button>
        <DownloadPdf />
      </div>
    </PageHero>
  );
}

function WhyNow() {
  return (
    <Section>
      <SectionTitle eyebrow="Why now" title="A few companies own the cloud. It keeps breaking." />
      <StatGrid stats={CLOUD_STATS} />
      <div className="mt-8">
        <OutageList />
      </div>
      <h3 className="mt-12 mb-4 font-display font-semibold text-fg">And Europe wants out.</h3>
      <StatGrid stats={SOVEREIGNTY_STATS} />
      <h3 className="mt-12 mb-4 font-display font-semibold text-fg">Meanwhile, people's keys live in four different apps.</h3>
      <StatGrid stats={WALLET_STATS} />
    </Section>
  );
}

function Competition() {
  return (
    <Section>
      <SectionTitle eyebrow="Competition" title="Nobody else does all of it." />
      <div className="flex flex-col gap-8">
        <CompetitionGrid grid={CLOUD_COMPETITION} caption="Orama compared with developer clouds and decentralized clouds" />
        <CompetitionGrid grid={WALLET_COMPETITION} caption="RootWallet compared with wallets and password managers" />
      </div>
    </Section>
  );
}

function Money() {
  return (
    <>
      <Section>
        <SectionTitle eyebrow="Business model" title="How Orama makes money." />
        <OramaRevenue />
      </Section>
      <Section>
        <SectionTitle eyebrow="Business model" title="How RootWallet makes money." />
        <WalletRevenue />
      </Section>
      <Section>
        <SectionTitle eyebrow="What it adds up to" title="Revenue when the next round is raised." />
        <Projections />
      </Section>
    </>
  );
}

function Round() {
  return (
    <>
      <Section>
        <SectionTitle eyebrow="The round" title={`${formatEur(FUNDING_TOTAL_EUR)}, and where it goes.`} />
        <RoundTerms mailto={INVESTOR_MAILTO} email={CONTACT_EMAIL} />
        <div className="mt-10">
          <FundingDonut />
        </div>
      </Section>
      <Section>
        <SectionTitle eyebrow="Timeline" title="From funding to the next round." />
        <div className="border border-dashed border-border p-6 sm:p-10">
          <RoundTimeline />
        </div>
        <h3 className="mt-10 mb-4 font-display font-semibold text-fg">What the round proves by month {FUNDING_MONTHS}</h3>
        <ProofPoints />
      </Section>
    </>
  );
}

function Hardware() {
  return (
    <Section>
      <SectionTitle eyebrow="Hardware" title={HARDWARE.name} />
      <div className="p-6 sm:p-8 border border-dashed border-signal/25">
        <StatusBadge tone="future" className="mb-5">
          {HARDWARE.status}
        </StatusBadge>
        <ul className="flex flex-col gap-3">
          {HARDWARE.points.map((p) => (
            <li key={p} className="flex items-start gap-3 text-accent">
              <span className="mt-2 w-1.5 h-1.5 rounded-full bg-signal shrink-0" />
              {p}
            </li>
          ))}
        </ul>
        <p className="mt-5 text-sm text-muted">{HARDWARE.next}</p>
      </div>
    </Section>
  );
}

function CaseAndRisks() {
  return (
    <>
      <Section>
        <SectionTitle eyebrow="Why we win" title="Hard to copy." />
        <CardList items={MOAT.map((m) => ({ title: m.title, line: m.line }))} />
      </Section>
      <Section>
        <SectionTitle eyebrow="Risks" title="What could go wrong, and our answer." />
        <CardList columns={2} items={RISKS.map((r) => ({ title: r.risk, line: r.answer }))} />
      </Section>
      <Section>
        <SectionTitle eyebrow="Comparables" title="What this category is worth." />
        <ul className="grid grid-cols-1 sm:grid-cols-2 gap-px bg-border border border-border">
          {COMPARABLES.map((c) => (
            <li key={c.what} className="flex flex-col gap-1 p-5 bg-surface sm:odd:last:col-span-2">
              <span className="font-display font-semibold text-fg">
                {c.what}
                <SourceRef ids={[c.source]} />
              </span>
              <span className="text-sm text-muted">{c.detail}</span>
            </li>
          ))}
        </ul>
      </Section>
      <Section>
        <SectionTitle eyebrow="Beyond equity" title="Grants we plan to apply for." line="Non-dilutive money that stretches the round." />
        <ul className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          {GRANTS.map((g) => (
            <li key={g.name} className="flex flex-col gap-2 p-5 border border-dashed border-border">
              <span className="font-display font-semibold text-fg">
                {g.name}
                <SourceRef ids={[g.source]} />
              </span>
              <span className="text-sm text-muted">{g.line}</span>
            </li>
          ))}
        </ul>
      </Section>
    </>
  );
}

function Faq() {
  return (
    <Section>
      <SectionTitle eyebrow="FAQ" title="Questions investors ask." />
      <div className="flex flex-col divide-y divide-dashed divide-border border-y border-dashed border-border">
        {FAQ.map((f) => (
          <details key={f.q} className="group py-4">
            <summary className="flex items-center justify-between gap-4 cursor-pointer list-none font-display font-semibold text-fg">
              {f.q}
              <Plus size={16} className="no-print shrink-0 text-muted transition-transform group-open:rotate-45" />
            </summary>
            <p className="pt-3 text-sm text-muted max-w-3xl">{f.a}</p>
          </details>
        ))}
      </div>
    </Section>
  );
}

function Closing() {
  return (
    <>
      <CtaBand title="Let's talk." line="Tell us who you are and what you'd like to know.">
        <Button asChild size="lg" className="rounded-full">
          <a href={INVESTOR_MAILTO}>
            {CONTACT_EMAIL}
            <ArrowRight className="w-3.5 h-3.5 ml-2" />
          </a>
        </Button>
        <DownloadPdf />
        <Button asChild variant="ghost" size="lg" className="rounded-full">
          <Link to={ROUTES.whitepaper.path}>Read the whitepaper</Link>
        </Button>
      </CtaBand>
      <Section padding="narrow" id="sources">
        <h2 className="font-mono text-[11px] tracking-[0.25em] uppercase text-muted mb-4">Sources</h2>
        <SourcesList />
        <p className="mt-6 text-xs text-muted max-w-3xl">Figures read from their sources on 24 September 2026. {DISCLAIMER}</p>
      </Section>
    </>
  );
}

export default function Investors() {
  return (
    <Page route={ROUTES.investors}>
      <Hero />
      <ThirtySeconds />
      <Section>
        <SectionTitle eyebrow="Already built" title="Two working products. Real users." />
        <ProductPair />
        <h3 className="mt-12 mb-4 font-display font-semibold text-fg">What three engineers shipped</h3>
        <Shipped />
      </Section>
      <Section>
        <SectionTitle eyebrow="Together" title="The keys and the cloud." line="Each product brings users to the other." />
        <Flywheel />
      </Section>
      <WhyNow />
      <Section>
        <SectionTitle eyebrow="Market" title="Where we start, and how big it gets." />
        <MarketLayers />
      </Section>
      <Competition />
      <Money />
      <Round />
      <Hardware />
      <CaseAndRisks />
      <Faq />
      <Section>
        <SectionTitle eyebrow="See for yourself" title="Everything we mention, one click away." />
        <SeeForYourself />
      </Section>
      <Closing />
    </Page>
  );
}
