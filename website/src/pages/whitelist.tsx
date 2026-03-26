import { useState, lazy, Suspense } from "react";
import { Link } from "react-router";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";
import { StatusDot } from "../components/ui/status-dot";
import { Button } from "../components/ui/button";
import { SplitText } from "../components/ui/split-text";
import { SILVER } from "../components/ui/silver-theme";

const NetworkVisualization = lazy(() =>
  import("../components/landing/network-visualization").then((m) => ({
    default: m.NetworkVisualization,
  })),
);
import {
  ChevronRight,
  Code,
  Database,
  Cpu,
  Globe,
  Layers,
  HardDrive,
  ArrowRight,
  MessageCircle,
  Bell,
  Rocket,
  Shield,
  Zap,
  ExternalLink,
} from "lucide-react";

/* ═══════════════════════════════════════════
   1. HERO
   ═══════════════════════════════════════════ */
function WaitlistHero() {
  return (
    <Section padding="wide">
      <div className="waitlist-hero flex flex-col items-center text-center min-h-[50vh] pt-[12vh] gap-6 max-w-3xl mx-auto">
        <span
          className="inline-flex items-center px-3 py-1 text-xs font-mono tracking-widest uppercase rounded-full"
          style={{ border: `1px dashed ${SILVER.mid}`, color: SILVER.light }}
        >
          WAITLIST
        </span>

        <h1 className="font-display font-bold text-4xl lg:text-6xl leading-tight">
          <SplitText
            text="Be the first to know."
            className="text-fg"
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
          />
          <br />
          <SplitText
            text="Build before everyone else."
            delay={30}
            duration={0.6}
            splitType="chars"
            from={{ opacity: 0, y: 30 }}
            to={{ opacity: 1, y: 0 }}
            className=""
          />
        </h1>

        <style>{`
          .waitlist-hero h1 > span:last-of-type .split-char {
            background: ${SILVER.gradient};
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
          }
        `}</style>

        <p className="text-muted max-w-lg text-sm leading-relaxed">
          Orama Network is preparing to launch. All announcements — token details,
          node license sales, developer access, and mainnet timelines — will be
          shared through our official channels first.
        </p>

        <div className="flex items-center gap-2">
          <StatusDot status="active" />
          <span className="text-xs font-mono text-muted tracking-wider uppercase">
            Waitlist is open
          </span>
        </div>

        <div className="flex flex-col sm:flex-row gap-3">
          <a
            href="https://t.me/debrosportal"
            target="_blank"
            rel="noopener noreferrer"
            className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm cursor-pointer text-black"
          >
            Join on Telegram
            <ArrowRight className="w-4 h-4 ml-2" />
          </a>
          <Button asChild variant="ghost" size="lg">
            <a href="https://x.com/debrosofficial" target="_blank" rel="noopener noreferrer">
              Follow on X
              <ExternalLink className="w-3.5 h-3.5 ml-2" />
            </a>
          </Button>
        </div>
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   2. HOW YOU'LL BE NOTIFIED
   ═══════════════════════════════════════════ */
function NotificationChannels() {
  const channels = [
    {
      icon: MessageCircle,
      title: "Telegram",
      description: "Our primary channel. All announcements, launch updates, and early access invitations are posted here first.",
      cta: "Join Telegram",
      href: "https://t.me/debrosportal",
      primary: true,
    },
    {
      icon: Globe,
      title: "X (Twitter)",
      description: "Follow @debrosofficial for public updates, threads on progress, and ecosystem news.",
      cta: "Follow on X",
      href: "https://x.com/debrosofficial",
      primary: false,
    },
    {
      icon: Shield,
      title: "AnChat Lite",
      description: "Already using AnChat? You'll receive notifications before anyone else. AnChat Lite users get priority early access to the network.",
      cta: "Open AnChat",
      href: "https://anchat.io",
      primary: false,
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="How You'll Be Notified"
          subtitle="Everything will be announced through our official channels. No surprises, no missed updates."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-8">
        {channels.map((ch) => (
          <AnimateIn key={ch.title}>
            <DashedPanel withCorners withBackground className={`h-full ${ch.primary ? "border-accent/30" : ""}`}>
              <div className="flex flex-col gap-4 p-4 h-full">
                <div className="flex items-center gap-3">
                  <ch.icon className="w-5 h-5 text-accent" />
                  <h3 className="font-display font-bold text-fg">{ch.title}</h3>
                  {ch.primary && (
                    <span className="text-[10px] font-mono tracking-wider uppercase px-2 py-0.5 rounded-full" style={{ border: `1px solid ${SILVER.border}`, color: SILVER.light }}>
                      PRIMARY
                    </span>
                  )}
                </div>
                <p className="text-muted text-sm leading-relaxed flex-1">{ch.description}</p>
                <a
                  href={ch.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-2 text-xs font-mono tracking-wider text-accent hover:text-fg transition-colors"
                >
                  {ch.cta}
                  <ExternalLink className="w-3 h-3" />
                </a>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   3. ANCHAT EARLY ACCESS
   ═══════════════════════════════════════════ */
function AnChatEarlyAccess() {
  const cyan = "#00A5AF";

  return (
    <Section>
      <AnimateIn>
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col md:flex-row items-center gap-8 p-6">
            <div className="flex flex-col gap-4 flex-1">
              <div className="flex items-center gap-3">
                <img src="/images/anchat.png" alt="AnChat" className="w-8 h-8 rounded-lg" />
                <span className="text-xs font-mono tracking-wider uppercase" style={{ color: cyan }}>
                  PRIORITY ACCESS
                </span>
              </div>
              <h3 className="font-display font-bold text-2xl text-fg">
                AnChat Lite users get notified first
              </h3>
              <p className="text-muted text-sm leading-relaxed">
                If you're already using AnChat Lite, you'll receive launch notifications
                and early access invitations before anyone else. AnChat is built entirely
                on the Orama Network — its users are our first community.
              </p>
              <div className="flex flex-wrap gap-2">
                <span className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono rounded-full" style={{ border: `1px solid ${cyan}30`, color: cyan, background: `${cyan}08` }}>
                  <Zap className="w-3 h-3" /> Early notifications
                </span>
                <span className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono rounded-full" style={{ border: `1px solid ${cyan}30`, color: cyan, background: `${cyan}08` }}>
                  <Shield className="w-3 h-3" /> Priority onboarding
                </span>
                <span className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono rounded-full" style={{ border: `1px solid ${cyan}30`, color: cyan, background: `${cyan}08` }}>
                  <Bell className="w-3 h-3" /> In-app alerts
                </span>
              </div>
              <a
                href="https://anchat.io"
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-6 py-2.5 text-xs rounded-sm cursor-pointer transition-all duration-200 w-fit"
                style={{ background: cyan, color: "#000" }}
              >
                Get AnChat Lite
                <ExternalLink className="w-3.5 h-3.5 ml-2" />
              </a>
            </div>
            <div className="w-full md:w-48 flex justify-center">
              <img src="/images/anchat-screens/1.png" alt="AnChat Preview" className="w-36 rounded-2xl shadow-lg" />
            </div>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   4. WHAT YOU CAN DEPLOY
   ═══════════════════════════════════════════ */
function WhatYouCanDeploy() {
  const services = [
    { icon: Globe, label: "React / Static Sites" },
    { icon: Database, label: "SQL Databases" },
    { icon: Cpu, label: "Go / Node.js APIs" },
    { icon: HardDrive, label: "File Storage (IPFS)" },
    { icon: Layers, label: "In-Memory Cache" },
    { icon: Code, label: "Serverless Functions" },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="What You'll Be Able to Deploy"
          subtitle="Everything you'd deploy on AWS — on decentralized infrastructure."
        />
      </AnimateIn>

      <div className="grid grid-cols-2 sm:grid-cols-3 gap-3 mt-8">
        {services.map((s) => (
          <AnimateIn key={s.label}>
            <DashedPanel withBackground>
              <div className="flex items-center gap-3 p-3">
                <s.icon className="w-4 h-4 text-accent shrink-0" />
                <span className="text-sm text-fg font-mono">{s.label}</span>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   5. WHAT'S COMING
   ═══════════════════════════════════════════ */
function WhatsNext() {
  const items = [
    {
      icon: Rocket,
      title: "Token & License Details",
      description: "Pricing, allocation, and vesting details will be announced on our official channels.",
    },
    {
      icon: Code,
      title: "Developer Access",
      description: "Waitlist members will be onboarded in batches. Early joiners get priority access.",
    },
    {
      icon: Globe,
      title: "Mainnet Launch",
      description: "Full mainnet with trading, staking, and governance. Timeline to be announced.",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="What's Coming"
          subtitle="These details will be announced soon — stay tuned."
        />
      </AnimateIn>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mt-8">
        {items.map((item) => (
          <AnimateIn key={item.title}>
            <DashedPanel withBackground className="h-full">
              <div className="flex flex-col gap-3 p-4">
                <item.icon className="w-5 h-5 text-accent" />
                <h3 className="font-display font-bold text-sm text-fg">{item.title}</h3>
                <p className="text-muted text-xs leading-relaxed">{item.description}</p>
              </div>
            </DashedPanel>
          </AnimateIn>
        ))}
      </div>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   6. FAQ
   ═══════════════════════════════════════════ */
function WaitlistFAQ() {
  const [openIndex, setOpenIndex] = useState<number | null>(null);

  const faqs = [
    {
      question: "How do I join the waitlist?",
      answer: "Join our Telegram group at t.me/debrosportal. All announcements and early access invitations are shared there first. If you use AnChat Lite, you'll get notified even earlier.",
    },
    {
      question: "When will I get access?",
      answer: "We'll onboard waitlist members in batches as we open the platform. Follow our Telegram and X for timeline updates. AnChat Lite users get priority.",
    },
    {
      question: "Do I need to pay anything?",
      answer: "No. The waitlist is free. When the platform opens, there will be a free tier with generous compute credits.",
    },
    {
      question: "Where will token and pricing details be announced?",
      answer: "All details about the $ORAMA token, node licenses, and pricing will be announced on our official Telegram and X accounts. AnChat Lite users will be notified in-app.",
    },
    {
      question: "Can I also invest or run a node?",
      answer: "Yes! Check the Investors page for information about backing the network. Node license and token details will be announced soon.",
    },
  ];

  return (
    <Section>
      <AnimateIn>
        <SectionHeader title="FAQ" subtitle="Common questions about the waitlist." />
      </AnimateIn>

      <div className="flex flex-col gap-2 mt-8 max-w-2xl mx-auto">
        {faqs.map((faq, i) => (
          <AnimateIn key={i}>
            <button
              type="button"
              onClick={() => setOpenIndex(openIndex === i ? null : i)}
              className="w-full text-left"
            >
              <DashedPanel withBackground>
                <div className="flex flex-col gap-0 p-2">
                  <div className="flex items-center justify-between gap-4">
                    <h3 className="font-display font-bold text-fg text-sm">{faq.question}</h3>
                    <ChevronRight
                      className={`w-4 h-4 shrink-0 text-muted transition-transform duration-200 ${
                        openIndex === i ? "rotate-90" : ""
                      }`}
                    />
                  </div>
                  <div
                    className={`overflow-hidden transition-all duration-300 ${
                      openIndex === i ? "max-h-40 mt-3 opacity-100" : "max-h-0 opacity-0"
                    }`}
                  >
                    <p className="text-muted text-sm leading-relaxed">{faq.answer}</p>
                  </div>
                </div>
              </DashedPanel>
            </button>
          </AnimateIn>
        ))}
      </div>

      {/* Final CTA */}
      <AnimateIn>
        <div className="flex flex-col items-center text-center gap-4 mt-12">
          <p className="text-muted text-sm">Don't miss the launch.</p>
          <div className="flex flex-wrap gap-3 justify-center">
            <a
              href="https://t.me/debrosportal"
              target="_blank"
              rel="noopener noreferrer"
              className="silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase px-8 py-3 text-sm rounded-sm cursor-pointer text-black"
            >
              Join Telegram
              <ArrowRight className="w-4 h-4 ml-2" />
            </a>
            <Button asChild variant="ghost" size="lg">
              <Link to="/investors">
                Become an Investor
                <ArrowRight className="w-3.5 h-3.5 ml-2" />
              </Link>
            </Button>
          </div>
        </div>
      </AnimateIn>
    </Section>
  );
}

/* ═══════════════════════════════════════════
   PAGE
   ═══════════════════════════════════════════ */
export default function Whitelist() {
  return (
    <Page title="Waitlist — Orama Network">
      <WaitlistHero />
      <div className="-mt-[200px]">
        <Suspense fallback={null}>
          <NetworkVisualization step={0} />
        </Suspense>
      </div>
      <Section padding="none"><CrosshairDivider /></Section>
      <NotificationChannels />
      <Section padding="none"><CrosshairDivider /></Section>
      <AnChatEarlyAccess />
      <Section padding="none"><CrosshairDivider /></Section>
      <WhatYouCanDeploy />
      <Section padding="none"><CrosshairDivider /></Section>
      <WhatsNext />
      <Section padding="none"><CrosshairDivider /></Section>
      <WaitlistFAQ />
    </Page>
  );
}
