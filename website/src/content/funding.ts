/**
 * The investment plan: €1,000,000 over 24 months. Allocations must sum to the
 * total — funding.test.ts enforces it so the donut and the headline figure can
 * never disagree.
 */
export const FUNDING_TOTAL_EUR = 1_000_000;
export const FUNDING_MONTHS = 24;

export interface Allocation {
  id: string;
  label: string;
  amountEur: number;
  delivers: string;
}

export const ALLOCATIONS: Allocation[] = [
  {
    id: "engineering",
    label: "Core engineering",
    amountEur: 400_000,
    delivers: "Stability and hardening",
  },
  {
    id: "orama-one",
    label: "Orama One hardware",
    amountEur: 200_000,
    delivers: "Final design and first batch",
  },
  {
    id: "oramaos",
    label: "OramaOS",
    amountEur: 150_000,
    delivers: "The node operating system",
  },
  {
    id: "audit",
    label: "Security audit",
    amountEur: 100_000,
    delivers: "Independent external review",
  },
  {
    id: "infrastructure",
    label: "Network infrastructure",
    amountEur: 50_000,
    delivers: "More nodes, more regions",
  },
  {
    id: "contributors",
    label: "Open-source contributors",
    amountEur: 50_000,
    delivers: "Bounties and documentation",
  },
  {
    id: "legal",
    label: "Swiss company & legal",
    amountEur: 50_000,
    delivers: "Incorporation and operations",
  },
];

export interface TimelineStop {
  month: number;
  title: string;
  line: string;
}

export const TIMELINE: TimelineStop[] = [
  { month: 0, title: "Funding", line: "Swiss company formed" },
  { month: 12, title: "Stable + OramaOS", line: "Roadmap steps 1 and 2" },
  { month: 18, title: "First Orama One batch", line: "Hardware built" },
  { month: 24, title: "Nodes to the public", line: "Usage-based revenue begins" },
];

const EUR = new Intl.NumberFormat("en-IE", {
  style: "currency",
  currency: "EUR",
  maximumFractionDigits: 0,
});

export function formatEur(amount: number): string {
  return EUR.format(amount);
}

/** Compact form for chart labels: 400000 -> "€400k", 1000000 -> "€1M". */
export function formatEurShort(amount: number): string {
  if (amount === 0) return "€0";
  const thousands = Math.round(amount / 1_000);
  // Round first, so 999,999 reads "€1M", not "€1000k".
  if (thousands >= 1_000) return `€${Math.round(thousands / 10) / 100}M`;
  return `€${thousands}k`;
}

/** The month usage-based revenue begins: the last stop on the timeline. */
export const REVENUE_MONTH = TIMELINE[TIMELINE.length - 1].month;
