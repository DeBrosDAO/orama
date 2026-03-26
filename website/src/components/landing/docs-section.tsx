import { Link } from "react-router";
import { BookOpen, ArrowRight, FileText, Terminal, Code } from "lucide-react";
import type { Persona } from "../../types/persona";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

interface DocLink {
  title: string;
  description: string;
  href: string;
  icon: React.ReactNode;
}

const docsContent: Record<Persona, { subtitle: string; links: DocLink[] }> = {
  developer: {
    subtitle: "Guides and references to help you deploy, manage, and scale on Orama.",
    links: [
      {
        title: "Getting Started",
        description: "Deploy your first app in under a minute",
        href: "/docs",
        icon: <BookOpen className="w-4 h-4" />,
      },
      {
        title: "CLI Reference",
        description: "Full command reference for the Orama CLI",
        href: "/docs",
        icon: <Terminal className="w-4 h-4" />,
      },
      {
        title: "SDK & APIs",
        description: "Integrate Orama services into your application",
        href: "/docs",
        icon: <Code className="w-4 h-4" />,
      },
    ],
  },
  operator: {
    subtitle: "Everything you need to set up, configure, and maintain your Orama node.",
    links: [
      {
        title: "Operator Setup Guide",
        description: "Install prerequisites and connect your node",
        href: "/docs/operator/getting-started",
        icon: <Terminal className="w-4 h-4" />,
      },
      {
        title: "Node Configuration",
        description: "Hardware requirements, ports, and environment setup",
        href: "/docs/operator/getting-started",
        icon: <FileText className="w-4 h-4" />,
      },
      {
        title: "Monitoring & Rewards",
        description: "Track uptime, performance, and earnings",
        href: "/docs/operator/getting-started",
        icon: <BookOpen className="w-4 h-4" />,
      },
    ],
  },
  contributor: {
    subtitle: "Architecture docs, contribution guidelines, and the full technical stack.",
    links: [
      {
        title: "Architecture Overview",
        description: "How the gateway, services, and mesh work together",
        href: "/docs",
        icon: <BookOpen className="w-4 h-4" />,
      },
      {
        title: "Contributing Guide",
        description: "Setup your dev environment and submit your first PR",
        href: "/docs",
        icon: <FileText className="w-4 h-4" />,
      },
      {
        title: "API Reference",
        description: "Internal APIs, endpoints, and data models",
        href: "/docs",
        icon: <Code className="w-4 h-4" />,
      },
    ],
  },
};

export function DocsSection({ persona }: { persona: Persona }) {
  const content = docsContent[persona];

  return (
    <>
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <SectionHeader title="Documentation" subtitle={content.subtitle} />

            <div className="grid grid-cols-1 md:grid-cols-3 gap-0">
              {content.links.map((doc) => (
                <Link
                  key={doc.title}
                  to={doc.href}
                  className="group border border-dashed border-border p-5 flex items-start gap-4 transition-all duration-200 hover:bg-surface-2/50 hover:border-border/80"
                >
                  <div className="text-muted group-hover:text-accent transition-colors shrink-0 mt-0.5">
                    {doc.icon}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <h3 className="font-display font-semibold text-fg text-sm">{doc.title}</h3>
                      <ArrowRight className="w-3 h-3 text-muted opacity-0 -translate-x-1 group-hover:opacity-100 group-hover:translate-x-0 transition-all" />
                    </div>
                    <p className="text-xs text-muted mt-1 leading-relaxed">{doc.description}</p>
                  </div>
                </Link>
              ))}
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>
    </>
  );
}
