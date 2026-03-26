import { Link } from "react-router";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";
import { Button } from "../components/ui/button";
import { SplitText } from "../components/ui/split-text";
import { SILVER } from "../components/ui/silver-theme";
import {
  ArrowRight,
  ExternalLink,
  Rocket,
  GraduationCap,
  Award,
  GitFork,
  MessageCircle,
  GitPullRequest,
  AlertTriangle,
  Code,
  Globe,
  Database,
  HardDrive,
  Shield,
  Cpu,
  Network,
  Box,
  Users,
} from "lucide-react";

/* ═══════════════════════════════════════════
   1. HERO
   ═══════════════════════════════════════════ */
function ContributorsHero() {
  return (
    <Section padding="wide">
      <div className="contributors-hero flex flex-col items-center text-center min-h-[70vh] pt-[12vh] gap-6 max-w-3xl mx-auto">
        <span
          className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full"
          style={{ border: `1px dashed ${SILVER.mid}`, color: SILVER.light }}
        >
          OPEN SOURCE
        </span>

        <h1 className="font-display font-bold text-4xl lg:text-6xl leading-tight">
          <SplitText
            text="Build the decentralized cloud."
            className="text-fg"
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
          />
          <br />
          <SplitText
            text="Ship code that matters."
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
            className=""
          />
        </h1>

        <style>{`
          .contributors-hero h1 > span:last-of-type .split-char {
            background: ${SILVER.gradient};
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
          }
        `}</style>

        <p className="text-muted text-sm leading-relaxed max-w-lg">
          Orama Network is open-source infrastructure built with Go and
          TypeScript. Contribute to a distributed system that powers real
          compute, storage, and networking across hundreds of nodes worldwide.
        </p>

        <div className="flex flex-wrap items-center gap-3 justify-center pt-4">
          <a
            href="https://github.com/orama-network"
            target="_blank"
            rel="noopener noreferrer"
            className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm cursor-pointer text-black"
          >
            View on GitHub
            <ExternalLink className="w-3.5 h-3.5 ml-2" />
          </a>
          <Button asChild variant="ghost" size="lg">
            <Link to="/docs">
              Read the Docs
              <ArrowRight className="w-3.5 h-3.5 ml-2" />
            </Link>
          </Button>
        </div>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   2. WHY CONTRIBUTE
   ═══════════════════════════════════════════ */
function WhyContribute() {
  const reasons = [
    {
      icon: Rocket,
      title: "Shape the Future",
      desc: "Your code runs on real infrastructure serving real users. This isn't a toy project — it's production distributed systems powering hundreds of nodes across three environments.",
    },
    {
      icon: GraduationCap,
      title: "Learn Distributed Systems",
      desc: "Work hands-on with WireGuard mesh networking, RQLite distributed SQL, Olric in-memory caching, IPFS decentralized storage, and WebAssembly serverless execution.",
    },
    {
      icon: Award,
      title: "Earn Recognition",
      desc: "Top contributors earn $ORAMA tokens and node licenses. Ship meaningful code, get recognized by the community, and earn a stake in the network you helped build.",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Why Contribute"
          subtitle="Join a growing community of engineers building the next generation of cloud infrastructure."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-4 mt-8">
        {reasons.map((item) => (
          <AnimateIn key={item.title}>
            <DashedPanel withBackground className="h-full">
              <div className="flex flex-col gap-3 p-2">
                <item.icon className="w-5 h-5 text-accent" />
                <h3 className="font-display font-bold text-fg">
                  {item.title}
                </h3>
                <p className="text-sm text-muted leading-relaxed">
                  {item.desc}
                </p>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   3. TECH STACK
   ═══════════════════════════════════════════ */
function TechStack() {
  const technologies = [
    {
      icon: Code,
      name: "Go",
      desc: "Core gateway, node orchestration, and CLI tooling",
    },
    {
      icon: Globe,
      name: "TypeScript",
      desc: "Website, dashboard, and developer SDK",
    },
    {
      icon: Database,
      name: "RQLite",
      desc: "Distributed SQL database with Raft consensus",
    },
    {
      icon: HardDrive,
      name: "Olric",
      desc: "In-memory distributed cache and DMap storage",
    },
    {
      icon: Box,
      name: "IPFS",
      desc: "Content-addressed decentralized file storage",
    },
    {
      icon: Shield,
      name: "WireGuard",
      desc: "Encrypted mesh VPN connecting all nodes",
    },
    {
      icon: Cpu,
      name: "WebAssembly",
      desc: "Sandboxed serverless function execution",
    },
    {
      icon: Network,
      name: "Orama Proxy",
      desc: "Privacy relay layer running on every node",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Tech Stack"
          subtitle="The tools and technologies that power the Orama Network."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mt-8">
        {technologies.map((tech) => (
          <AnimateIn key={tech.name}>
            <DashedPanel withBackground className="h-full">
              <div className="flex flex-col gap-2 p-2">
                <div className="flex items-center gap-2">
                  <tech.icon className="w-4 h-4 text-accent" />
                  <span className="font-mono text-sm font-semibold text-fg tracking-wider">
                    {tech.name}
                  </span>
                </div>
                <p className="text-xs text-muted leading-relaxed">
                  {tech.desc}
                </p>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   4. HOW TO START
   ═══════════════════════════════════════════ */
function HowToStart() {
  const steps = [
    {
      number: "01",
      icon: MessageCircle,
      title: "Join AnChat",
      desc: "Go to anchat.io, create your account with your crypto wallet, and join the DeBros group. This is where the core team coordinates all development work.",
      link: "https://anchat.io",
    },
    {
      number: "02",
      icon: Users,
      title: "Message the Team",
      desc: "Reach out to DeBros on AnChat and introduce yourself. Tell us what you're good at and what you'd like to work on. We'll guide you to the right area of the codebase.",
    },
    {
      number: "03",
      icon: GitFork,
      title: "Get Assigned a Task",
      desc: "The core team will assign you a task that matches your skills and the current priorities. You'll get context on the architecture, dependencies, and expected behavior.",
    },
    {
      number: "04",
      icon: GitPullRequest,
      title: "Submit Your PR",
      desc: "Write your code with guidance from the team. Include tests, keep commits clean, and open a pull request. The core team reviews every PR with fast turnaround.",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="How to Contribute"
          subtitle="Get guidance from the core team before writing code."
        />
      </AnimateIn>

      {/* Early stage callout */}
      <AnimateIn>
        <div
          className="flex items-start gap-3 px-4 py-3 rounded-lg mt-8 mb-4"
          style={{ background: "rgba(234,179,8,0.06)", border: "1px solid rgba(234,179,8,0.2)" }}
        >
          <AlertTriangle className="w-4 h-4 shrink-0 mt-0.5 text-yellow-500" />
          <span className="text-xs font-mono text-yellow-500/90 leading-relaxed">
            Orama Network is in early stage with frequent breaking changes. We strongly recommend
            contacting the core team via AnChat before starting any contribution — we'll make sure
            you're working on the right thing with the latest context.
          </span>
        </div>
      </AnimateIn>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mt-8">
        {steps.map((step) => (
          <AnimateIn key={step.number}>
            <DashedPanel withBackground className="h-full">
              <div className="flex flex-col gap-3 p-3">
                <div className="flex items-center gap-3">
                  <span
                    className="font-mono text-2xl font-bold"
                    style={{
                      background: SILVER.gradient,
                      WebkitBackgroundClip: "text",
                      WebkitTextFillColor: "transparent",
                    }}
                  >
                    {step.number}
                  </span>
                  <step.icon className="w-5 h-5 text-accent" />
                </div>
                <h3 className="font-display font-bold text-fg">
                  {step.title}
                </h3>
                <p className="text-sm text-muted leading-relaxed">
                  {step.desc}
                </p>
                {"link" in step && step.link && (
                  <a
                    href={step.link}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1.5 text-xs font-mono text-accent hover:text-fg transition-colors tracking-wider uppercase"
                  >
                    anchat.io
                    <ExternalLink className="w-3 h-3" />
                  </a>
                )}
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   5. CTA
   ═══════════════════════════════════════════ */
function ContributorCta() {
  return (
    <Section>
      <AnimateIn>
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col items-center text-center gap-6 py-12 px-6">
            <Users className="w-8 h-8 text-accent" />
            <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
              Ready to contribute?
            </h2>
            <p className="text-muted text-sm leading-relaxed max-w-md">
              Join AnChat, message the DeBros team, and start building the
              decentralized cloud with guidance from the core developers.
            </p>
            <div className="flex flex-wrap items-center gap-3 justify-center">
              <a
                href="https://anchat.io"
                target="_blank"
                rel="noopener noreferrer"
                className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm cursor-pointer text-black"
              >
                Join AnChat
                <ExternalLink className="w-3.5 h-3.5 ml-2" />
              </a>
              <Button asChild variant="ghost" size="lg">
                <a
                  href="https://github.com/DeBrosOfficial"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  Browse the Code
                  <ExternalLink className="w-3.5 h-3.5 ml-2" />
                </a>
              </Button>
            </div>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   PAGE
   ═══════════════════════════════════════════ */
export default function ContributorsLanding() {
  return (
    <Page title="Contribute to Orama — Open Source Infrastructure">
      <ContributorsHero />
      <CrosshairDivider />
      <WhyContribute />
      <CrosshairDivider />
      <TechStack />
      <CrosshairDivider />
      <HowToStart />
      <CrosshairDivider />
      <ContributorCta />
    </Page>
  );
}
