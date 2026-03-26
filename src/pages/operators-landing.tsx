import { Page } from "../components/layout/page";
import { LandingHero } from "../components/landing/hero";
import { OpsTokenomics } from "../components/landing/ops-tokenomics";
import { OpsAnyone } from "../components/landing/ops-anyone";
import { OpsOramaOne } from "../components/landing/ops-orama-one";
import { OpsSetup } from "../components/landing/ops-setup";
import { CtaSection } from "../components/landing/cta-section";
import { DocsSection } from "../components/landing/docs-section";

export default function OperatorsLanding() {
  return (
    <Page title="Node Operators — Power the Decentralized Cloud">
      <LandingHero persona="operator" />
      <OpsTokenomics />
      <OpsAnyone />
      <OpsOramaOne />
      <OpsSetup />
      <DocsSection persona="operator" />
      <CtaSection persona="operator" />
    </Page>
  );
}
