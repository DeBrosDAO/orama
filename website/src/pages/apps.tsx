import { Link } from "react-router";
import { ArrowRight } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { PageHero } from "../components/ui/page-hero";
import { Button } from "../components/ui/button";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AppShowcaseBlock } from "../components/visuals/app-showcase";
import { CtaBand } from "../components/sections/cta-band";
import { ROUTES } from "../content/routes";

export default function Apps() {
  return (
    <Page route={ROUTES.apps}>
      <PageHero
        eyebrow="Apps"
        title="Real apps. Real people."
        line="Orama isn't a slide deck. AnChat runs on it today; RootWallet is how you sign in to it."
      />

      <Section padding="narrow" id="anchat">
        <AppShowcaseBlock id="anchat" />
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      <Section id="rootwallet">
        <AppShowcaseBlock id="rootwallet" />
      </Section>

      <CtaBand title="Your app could be next." line="Orama is early. The platform is open source and ready to explore.">
        <Button asChild size="lg">
          <Link to={ROUTES.platform.path}>See the platform<ArrowRight className="w-3.5 h-3.5 ml-2" /></Link>
        </Button>
        <Button asChild variant="ghost" size="lg">
          <Link to={ROUTES.useCases.path}>Use cases</Link>
        </Button>
      </CtaBand>
    </Page>
  );
}
