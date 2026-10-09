import { Link } from "react-router";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { DOCS_PATH, PERSONA_LABEL, docPath } from "../content/pages";
import { PERSONA_DOCS } from "../data/docs-navigation";
import type { Persona } from "../types/persona";

const PERSONA_LINE: Record<Persona, string> = {
  start: "What Orama is, what works today, and where to begin.",
  developer: "Deploy apps, databases, storage and functions with the CLI and SDK.",
  operator: "Set up, run, monitor and upgrade an Orama cluster, from empty servers.",
  architecture: "How the parts work inside, and the security model.",
  blockchain: "The Orama ledger: supply, fees, validators and running a chain node.",
  privacy: "The private Tor network, relayed fetch and the VPN client.",
  rootwallet: "The wallet and agent that sign you in, on desktop and mobile.",
  contributor: "How the code is laid out, built, tested and released.",
};

const PERSONAS = Object.keys(PERSONA_DOCS) as Persona[];

/** The documentation's front page: every doc, grouped by who it is for. */
export default function DocsHome() {
  return (
    <Page route={{ path: DOCS_PATH, title: "Documentation", description: "" }}>
      <PageHero
        eyebrow="Documentation"
        title="Build on Orama. Run a node."
        line="Everything in the docs, grouped by who it is for."
      />
      <Section padding="narrow" className="pb-24">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {PERSONAS.map((persona) => (
            <section key={persona} className="flex flex-col gap-4 border border-dashed border-border p-6 sm:p-8">
              <div className="flex flex-col gap-1.5">
                <h2 className="font-display font-semibold text-xl text-fg">{PERSONA_LABEL[persona]}</h2>
                <p className="text-sm text-muted">{PERSONA_LINE[persona]}</p>
              </div>
              <ul className="flex flex-col">
                {PERSONA_DOCS[persona].map((link) => (
                  <li key={link.slug}>
                    <Link
                      to={docPath(link.slug)}
                      className="flex items-baseline justify-between gap-4 py-2 border-b border-dashed border-border/60 group"
                    >
                      <span className="text-sm text-fg group-hover:text-accent transition-colors">{link.title}</span>
                      <span className="text-xs text-muted text-right">{link.description}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      </Section>
    </Page>
  );
}
