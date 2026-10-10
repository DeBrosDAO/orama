/**
 * The round: €1,500,000 of equity in one Swiss company that owns both Orama
 * Network and RootWallet. Allocations must sum to the total — funding.test.ts
 * enforces it so the donut and the headline figure can never disagree.
 * The valuation is deliberately not here: it is not published.
 */
export const FUNDING_TOTAL_EUR = 1_500_000;
export const FUNDING_MONTHS = 18;
export const MONTHLY_BURN_EUR = FUNDING_TOTAL_EUR / FUNDING_MONTHS;

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
    amountEur: 700_000,
    delivers: "Orama and RootWallet, a team of about seven",
  },
  {
    id: "orama-one",
    label: "Orama One",
    amountEur: 200_000,
    delivers: "Founder Edition, certification, pilot batch",
  },
  {
    id: "audits",
    label: "Security audits",
    amountEur: 175_000,
    delivers: "Independent network and wallet audits",
  },
  {
    id: "oramaos",
    label: "OramaOS",
    amountEur: 150_000,
    delivers: "The locked-down node operating system",
  },
  {
    id: "ecosystem",
    label: "Ecosystem & growth",
    amountEur: 125_000,
    delivers: "Bounties, developer grants, community",
  },
  {
    id: "launch",
    label: "Launch & infrastructure",
    amountEur: 100_000,
    delivers: "Network capacity, app stores, code signing",
  },
  {
    id: "legal",
    label: "Swiss company & legal",
    amountEur: 50_000,
    delivers: "Swiss AG in Zug, operations",
  },
];

export interface TimelineStop {
  month: number;
  title: string;
  line: string;
}

/** The month paying customers start: Orama's paid beta. */
export const PAID_BETA_MONTH = 8;

/** RootWallet's monthly-user target at month 18. */
export const WALLET_USER_TARGET = 25_000;

export const TIMELINE: TimelineStop[] = [
  { month: 0, title: "Funding", line: "Swiss AG formed in Zug" },
  { month: PAID_BETA_MONTH, title: "Paid beta", line: "Developers start paying for Orama" },
  { month: 12, title: "Stable + OramaOS", line: "Measured uptime, OramaOS on real hardware" },
  { month: 15, title: "Raise the next round", line: "On early proof points, with runway left" },
  { month: 18, title: "Proof points met", line: "The results below, before the money runs out" },
];


/** What the round must prove by month 18, per product. */
export const PROOF_POINTS: { product: "Orama" | "RootWallet"; items: string[] }[] = [
  {
    product: "Orama",
    items: [
      `Paid beta open at month ${PAID_BETA_MONTH}`,
      "99.9% uptime, measured on a public status page",
      "50+ paying teams and the first support contract",
      "OramaOS running on real hardware",
      "First Orama One batch delivered",
    ],
  },
  {
    product: "RootWallet",
    items: [
      "Public launch in Q1 2027",
      "Independent security audit",
      "App Store and Google Play",
      `${WALLET_USER_TARGET.toLocaleString("en-US")} monthly users`,
      "Revenue live from swaps and Premium",
    ],
  },
];

const EUR = new Intl.NumberFormat("en-IE", {
  style: "currency",
  currency: "EUR",
  maximumFractionDigits: 0,
});

export function formatEur(amount: number): string {
  return EUR.format(amount);
}

/** Compact form for chart labels: 400000 -> "€400k", 1500000 -> "€1.5M". */
export function formatEurShort(amount: number): string {
  if (amount === 0) return "€0";
  const thousands = Math.round(amount / 1_000);
  // Round first, so 999,999 reads "€1M", not "€1000k".
  if (thousands >= 1_000) return `€${Math.round(thousands / 10) / 100}M`;
  return `€${thousands}k`;
}
