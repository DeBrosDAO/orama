import { Link } from "react-router";
import { Check, ChevronDown } from "lucide-react";
import { useState } from "react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { SpecTable } from "../components/ui/spec-table";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";

interface PricingPlan {
  name: string;
  badge: string;
  period: string;
  features: React.ReactNode[];
  cta: string;
  ctaLink: string;
  highlighted?: boolean;
}

const PLANS: PricingPlan[] = [
  {
    name: "Free",
    badge: "FREE",
    period: "/mo",
    features: [
      "100 requests/hour",
      "2,400 requests/day",
      "1 deployment (static only)",
      "100 MB storage (IPFS)",
      "10 database queries/min",
      "Community support",
    ],
    cta: "Start Free",
    ctaLink: "/docs",
  },
  {
    name: "Developer",
    badge: "DEV",
    period: "/mo",
    features: [
      "1,000 requests/hour",
      "3 deployments (static + SSR)",
      "1 GB storage (IPFS)",
      "100 database queries/min",
      "256 MB cache (Olric)",
      "100 serverless invocations/min",
      "Email support",
    ],
    cta: "Get Started",
    ctaLink: "/docs",
  },
  {
    name: "Pro",
    badge: "PRO",
    period: "/mo",
    highlighted: true,
    features: [
      "10,000 requests/hour",
      "10 deployments (all types)",
      "10 GB storage (IPFS)",
      "1,000 database queries/min",
      "1 GB cache (Olric)",
      "1,000 serverless invocations/min",
      "Custom domains",
      "Priority support",
    ],
    cta: "Get Started",
    ctaLink: "/docs",
  },
  {
    name: "Enterprise",
    badge: "ENTERPRISE",
    period: "",
    features: [
      "Unlimited requests",
      "Unlimited deployments",
      "100 GB storage",
      "Unlimited queries",
      "10 GB cache",
      "Unlimited serverless",
      "Dedicated nodes",
      "SLA guarantee",
    ],
    cta: "Contact Us",
    ctaLink: "/about",
  },
];

const PAY_PER_USE_ROWS = [
  { label: "$ORAMA / BTC Transfer", value: "1,000 rays (0.001 $ORAMA)" },
  { label: "SQL Query", value: "500 rays" },
  { label: "IPFS Storage", value: "10,000 rays per MB" },
  { label: "KV Store Read/Write", value: "200 rays" },
  { label: "WASM Execution", value: "1,000 rays per 1M instructions" },
  { label: "DEX Order Book Trade", value: "1,000 rays" },
  { label: "Private Transaction (zk-SNARK)", value: "4x the public equivalent" },
];

interface FaqItem {
  question: string;
  answer: React.ReactNode;
}

const FAQ_ITEMS: FaqItem[] = [
  {
    question: "What payment methods do you accept?",
    answer:
      "All plans can be paid in $ORAMA or BTC. Payments are processed on-chain via smart contracts. No credit cards or bank transfers required.",
  },
  {
    question: "Can I switch plans at any time?",
    answer:
      "Yes. Upgrade or downgrade at any time. When upgrading, you only pay the prorated difference for the remainder of the billing cycle. Downgrades take effect at the next billing period.",
  },
  {
    question: "What happens if I exceed my plan limits?",
    answer:
      "Requests beyond your plan limit are rate-limited, not billed. You can upgrade your plan or enable pay-as-you-go overage billing to avoid disruptions.",
  },
  {
    question: "Is there a commitment or lock-in?",
    answer:
      "No. All plans are month-to-month with no long-term contracts. Cancel anytime. Your data remains accessible on IPFS even after cancellation.",
  },
];

function FaqAccordion({ items }: { items: FaqItem[] }) {
  const [openIndex, setOpenIndex] = useState<number | null>(null);

  return (
    <div className="border border-dashed border-border">
      {items.map((item, i) => (
        <div
          key={i}
          className={i < items.length - 1 ? "border-b border-dashed border-border" : ""}
        >
          <button
            onClick={() => setOpenIndex(openIndex === i ? null : i)}
            className="w-full flex items-center justify-between px-4 sm:px-6 py-4 text-left cursor-pointer group"
          >
            <span className="font-display font-semibold text-sm text-fg group-hover:text-accent transition-colors">
              {item.question}
            </span>
            <ChevronDown
              className={`w-4 h-4 text-muted shrink-0 ml-4 transition-transform duration-200 ${
                openIndex === i ? "rotate-180" : ""
              }`}
            />
          </button>
          <div
            className={`overflow-hidden transition-all duration-200 ${
              openIndex === i ? "max-h-48" : "max-h-0"
            }`}
          >
            <p className="px-4 sm:px-6 pb-4 text-sm text-muted leading-relaxed">
              {item.answer}
            </p>
          </div>
        </div>
      ))}
    </div>
  );
}

export default function Pricing() {
  return (
    <Page title="Pricing">
      {/* Hero */}
      <Section padding="wide">
        <div className="flex flex-col gap-6 max-w-2xl">
          <Badge variant="default" className="w-fit">
            PRICING
          </Badge>
          <h1 className="font-display font-bold text-4xl lg:text-5xl leading-tight text-fg">
            Simple, transparent{" "}
            <span className="text-accent">pricing</span>
          </h1>
          <p className="text-muted text-lg leading-relaxed">
            Pay only for what you use. No surprise bills. No vendor lock-in.
          </p>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Pricing Grid */}
      <Section>
        <AnimateIn>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            {PLANS.map((plan) => (
              <DashedPanel
                key={plan.name}
                withCorners
                className={`p-5 sm:p-6 flex flex-col ${
                  plan.highlighted ? "border-accent" : ""
                }`}
              >
                <div className="flex flex-col gap-5 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="font-display font-semibold text-fg">
                      {plan.name}
                    </span>
                    {plan.highlighted && (
                      <Badge variant="outline" className="text-[10px]">
                        POPULAR
                      </Badge>
                    )}
                  </div>

                  <div className="flex items-baseline gap-1">
                    <span className="font-mono text-3xl font-bold text-fg">
                      {plan.name === "Free" ? "$0" : plan.name === "Developer" ? "$5" : plan.name === "Pro" ? "$25" : "Custom"}
                    </span>
                    {plan.period && (
                      <span className="text-muted text-sm">{plan.period}</span>
                    )}
                  </div>

                  <ul className="flex flex-col gap-2.5 flex-1">
                    {plan.features.map((feature, i) => (
                      <li
                        key={i}
                        className="flex items-start gap-2 text-sm text-muted"
                      >
                        <Check className="w-3.5 h-3.5 text-accent shrink-0 mt-0.5" />
                        {feature}
                      </li>
                    ))}
                  </ul>

                  <Button
                    asChild
                    variant={plan.highlighted ? "primary" : "ghost"}
                    size="lg"
                    className="w-full mt-2"
                  >
                    <Link to={plan.ctaLink}>{plan.cta}</Link>
                  </Button>
                </div>
              </DashedPanel>
            ))}
          </div>
        </AnimateIn>

        {/* Payment note */}
        <p className="text-xs text-muted text-center mt-6">
          All paid plans payable in $ORAMA or BTC
        </p>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Pay Per Use */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader
            title="Or pay as you go"
            subtitle="No plan required. Pay per unit for exactly what you consume."
          />
          <AnimateIn>
            <SpecTable rows={PAY_PER_USE_ROWS} />
          </AnimateIn>
          <p className="text-xs text-muted italic text-center">
            1 $ORAMA = 1,000,000 rays. All fees in $ORAMA. Base fee burned. Priority fee to block proposer.
          </p>
          <AnimateIn>
            <DashedPanel withBackground className="mt-4">
              <div className="flex flex-col gap-3 p-4">
                <span className="font-display font-semibold text-fg text-sm">Congestion Multiplier (EIP-1559 style)</span>
                <p className="text-xs text-muted leading-relaxed">
                  Fees adjust dynamically based on block fullness. At 50% capacity (~500 transactions per block),
                  the multiplier is 1x. As blocks fill toward the 1,000 transaction limit, the multiplier rises
                  (up to 10x). When blocks are under half full, it drops below 1x. This prevents spam during
                  peak demand and keeps fees low during normal usage.
                </p>
                <div className="flex flex-wrap gap-4 text-xs font-mono text-muted">
                  <span>Base fee &rarr; burned</span>
                  <span>Priority fee &rarr; block proposer</span>
                  <span>Fee schedule &rarr; governance-adjustable</span>
                </div>
              </div>
            </DashedPanel>
          </AnimateIn>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* FAQ */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="Frequently Asked Questions" />
          <AnimateIn>
            <FaqAccordion items={FAQ_ITEMS} />
          </AnimateIn>
        </div>
      </Section>

      {/* Disclaimer */}
      <Section padding="narrow">
        <p className="text-xs text-muted italic text-center">
          Pricing is subject to change. All payments are processed on-chain.
        </p>
      </Section>
    </Page>
  );
}
