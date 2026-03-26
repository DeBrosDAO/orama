import { ExternalLink } from "lucide-react";
import { Link } from "react-router";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { DashedPanel } from "../ui/dashed-panel";
import { Button } from "../ui/button";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

const repos = [
  {
    name: "Core Network",
    url: "https://github.com/DeBrosOfficial/network",
  },
  {
    name: "TypeScript SDK",
    url: "https://github.com/DeBrosOfficial/network-ts-sdk",
  },
  {
    name: "RootWallet",
    url: "https://github.com/DeBrosOfficial/rootwallet",
  },
  {
    name: "Orama Vault",
    url: "https://github.com/DeBrosOfficial/orama-vault",
  },
];

export function ContribOpenSource() {
  return (
    <>
      <Section>
        <AnimateIn>
        <div className="flex flex-col gap-8">
          <SectionHeader title="Pick an issue. Ship a fix." />

          <DashedPanel withCorners withBackground>
            <div className="flex flex-col gap-6">
              <p className="text-muted leading-relaxed">
                We pair-program with new contributors. Every PR gets reviewed.
                No question is too basic. The codebase has comprehensive
                documentation, strict code style guides, and a test suite with
                CI/CD.
              </p>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                {repos.map((repo) => (
                  <Button
                    key={repo.name}
                    asChild
                    variant="ghost"
                    className="justify-start"
                  >
                    <a
                      href={repo.url}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      {repo.name}
                      <ExternalLink className="w-3.5 h-3.5 ml-2" />
                    </a>
                  </Button>
                ))}
              </div>

              <div className="flex flex-wrap gap-3">
                <Button asChild variant="ghost">
                  <Link to="/docs/contributor/dev-setup">
                    Development Setup Guide
                  </Link>
                </Button>
                <Button asChild variant="ghost">
                  <Link to="/docs/contributor/code-style">
                    Code Style Guide
                  </Link>
                </Button>
              </div>
            </div>
          </DashedPanel>
        </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>
    </>
  );
}
