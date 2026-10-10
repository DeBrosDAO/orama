import { PAID_BETA_MONTH, WALLET_USER_TARGET } from "../funding";

export { WALLET_USER_TARGET };

/**
 * How the company makes money. Every scenario is illustrative and says so on
 * the page: explicit assumptions, not a forecast. Prices that are not decided
 * (RootWallet Premium/Pro) are never shown.
 */

export interface RevenueLine {
  title: string;
  line: string;
  when: string;
}

export const ORAMA_REVENUE: RevenueLine[] = [
  { title: "Usage", line: "Apps pay for what they use. Usage fees pay for node capacity; Orama keeps a 30% platform fee.", when: `From the paid beta, month ${PAID_BETA_MONTH}` },
  { title: "Per-app and team plans", line: "A small monthly fee per app and per team, because every app runs on three machines.", when: "From the paid beta" },
  { title: "Private networks", line: "Organisations run Orama on their own servers with an annual support contract per node.", when: "First contract by month 18" },
  { title: "Orama One", line: "Hardware sold as your own private cloud box, on refundable pre-orders.", when: "First batch by month 18" },
];

/** What the planned price sheet means against named competitors. */
export const ORAMA_PRICING = {
  headline: "Planned: about half of Railway's price",
  line: "on compute and data transfer, with three copies of your database for what others charge for one. Wallet sign-in is free.",
  sources: ["railway-pricing", "supabase-pricing"],
  note: "Planned pricing, to be tested on the network before launch.",
} as const;

export interface Scenario {
  id: "conservative" | "base" | "upside";
}

export const ROOTWALLET_REVENUE: RevenueLine[] = [
  { title: "Swaps & bridges", line: "A small fee when users trade tokens or move them between chains.", when: "From public launch, Q1 2027" },
  { title: "Buying crypto", line: "A partner share when users buy with a card.", when: "After launch" },
  { title: "Staking", line: "A commission on staking rewards.", when: "After launch" },
  { title: "Premium and Pro", line: "The free plan is a simple wallet. The full vault is Premium. Prices set at launch.", when: "From public launch" },
];

/** Annual fee revenue per monthly user at comparable wallets: revenue ÷ users. */
export const WALLET_BENCHMARKS = [
  { name: "MetaMask", value: "~$1.80", basis: "2025 fees ÷ 30M monthly users", sources: ["defillama-metamask", "metamask"] },
  { name: "Phantom", value: "~$8–22", basis: "2025–26 fees ÷ 15M+ monthly users", sources: ["defillama-phantom", "phantom"] },
  { name: "Exodus", value: "~$53–81", basis: "FY2025 revenue (incl. B2B swaps) ÷ 1.5–2.3M monthly users", sources: ["exodus"] },
] as const;

export interface WalletAssumptions {
  swappersShare: number;
  swapsPerSwapper: number;
  avgSwapUsd: number;
  swapFee: number;
  netAfterAggregator: number;
  buyersShare: number;
  avgBuyUsd: number;
  buyMarkup: number;
  stakersShare: number;
  avgStakedUsd: number;
  stakingApy: number;
  stakingCommission: number;
}

export const WALLET_SCENARIOS: Record<Scenario["id"], WalletAssumptions> = {
  conservative: { swappersShare: 0.04, swapsPerSwapper: 2, avgSwapUsd: 200, swapFee: 0.005, netAfterAggregator: 0.85, buyersShare: 0.01, avgBuyUsd: 150, buyMarkup: 0.005, stakersShare: 0.03, avgStakedUsd: 1500, stakingApy: 0.06, stakingCommission: 0.05 },
  base: { swappersShare: 0.08, swapsPerSwapper: 3, avgSwapUsd: 250, swapFee: 0.0075, netAfterAggregator: 0.85, buyersShare: 0.02, avgBuyUsd: 150, buyMarkup: 0.0075, stakersShare: 0.05, avgStakedUsd: 1500, stakingApy: 0.06, stakingCommission: 0.075 },
  upside: { swappersShare: 0.12, swapsPerSwapper: 4, avgSwapUsd: 300, swapFee: 0.0085, netAfterAggregator: 0.85, buyersShare: 0.03, avgBuyUsd: 200, buyMarkup: 0.01, stakersShare: 0.08, avgStakedUsd: 2000, stakingApy: 0.06, stakingCommission: 0.1 },
};

/** Annual fee revenue per monthly user, in USD. Fees only: no subscriptions. */
export function walletRevenuePerUser(a: WalletAssumptions): number {
  const months = 12;
  const swaps = a.swappersShare * a.swapsPerSwapper * a.avgSwapUsd * months * a.swapFee * a.netAfterAggregator;
  const buys = a.buyersShare * a.avgBuyUsd * a.buyMarkup * months;
  const staking = a.stakersShare * a.avgStakedUsd * a.stakingApy * a.stakingCommission;
  return swaps + buys + staking;
}

export const WALLET_BASE_ASSUMPTIONS = [
  "8% of monthly users swap, 3 times a month, $250 per swap",
  "0.75% swap fee; 85% kept after the routing partner's share",
  "2% buy crypto each month, $150 per purchase, 0.75% partner share",
  "5% stake $1,500 at 6% a year; 7.5% commission",
] as const;

/** Orama One in this round: a small, pre-order-funded first edition. */
export const HARDWARE = {
  name: "Orama One, Founder Edition",
  status: "Planned for this round",
  points: [
    "A ready-made mini PC with memory encryption, to run OramaOS",
    "100–300 units, built only against refundable pre-orders",
    "Sold as your own private cloud box. Joining the network is optional, under a separate agreement",
    "No earnings promises, ever",
  ],
  next: "The custom Orama One design is funded by the next round, once OramaOS is proven in the field.",
} as const;

export const GRANTS = [
  { name: "EU EIC Accelerator", line: "Grants of up to €2.5M for deep-tech startups.", source: "eic" },
  { name: "Innosuisse", line: "Covers up to 70% of an innovation project's direct costs.", source: "innosuisse" },
] as const;
