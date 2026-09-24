/**
 * Why now, and how big. Each figure carries the id of its source in
 * sources.ts; estimates say so in their note.
 */
export interface Stat {
  value: string;
  label: string;
  note?: string;
  source: string;
}

/** The cloud side: concentration and what it costs when it fails. */
export const CLOUD_STATS: Stat[] = [
  { value: "$419B", label: "spent on cloud infrastructure in 2025", note: "Q4 2025: +30% year on year", source: "synergy-2025" },
  { value: "63%", label: "held by Amazon, Microsoft and Google", note: "share in Q4 2025", source: "synergy-2025" },
  { value: "+43%", label: "cloud growth in Q2 2026, the fastest in eight years", source: "synergy-q2-2026" },
];

export interface Outage {
  when: string;
  what: string;
  impact: string;
  source: string;
}

export const OUTAGES: Outage[] = [
  { when: "Oct 2025", what: "AWS us-east-1", impact: "An estimated ~70,000 organisations hit", source: "aws-oct-2025" },
  { when: "Nov 2025", what: "Cloudflare", impact: "Its worst outage since 2019", source: "cloudflare-nov-2025" },
  { when: "May 2026", what: "AWS us-east-1", impact: "A data-center cooling failure took services down", source: "aws-may-2026" },
  { when: "May 2026", what: "Google Cloud vs Railway", impact: "Google suspended a whole developer cloud's account without warning: ~5 hours of disruption", source: "railway-suspended" },
];

/** The sovereignty side: Europe wants off the hyperscalers. */
export const SOVEREIGNTY_STATS: Stat[] = [
  { value: "+83%", label: "European sovereign cloud (IaaS) spending in 2026", note: "to $12.6B", source: "gartner-sovereign" },
  { value: "61%", label: "of Western European CIOs will rely more on local cloud", source: "gartner-cio" },
  { value: "Jan 2027", label: "the EU bans cloud switching fees", source: "data-act" },
];

/** The wallet side: RootWallet's market. */
export const WALLET_STATS: Stat[] = [
  { value: "774M", label: "people own crypto", note: "estimate, June 2026", source: "cryptocom" },
  { value: "40–70M", label: "are active users", note: "estimate, 2025", source: "a16z" },
  { value: "$400M+", label: "a year earned by 1Password alone", note: "ARR, Nov 2025", source: "1password" },
];

export interface MarketLayer {
  value: string;
  label: string;
  line: string;
  sources: string[];
}

/** From the whole market down to where Orama starts. */
export const MARKET_LAYERS: MarketLayer[] = [
  {
    value: "$419B",
    label: "Cloud infrastructure",
    line: "Everything businesses rent from the big clouds, 2025.",
    sources: ["synergy-2025"],
  },
  {
    value: "~$4–5B",
    label: "Independent developer clouds",
    line: "Cloudflare, DigitalOcean, Vercel, Supabase and peers, growing 30%+ a year. Our sum of public and estimated revenue.",
    sources: ["cloudflare-fy", "digitalocean-fy", "vercel-arr", "supabase-arr"],
  },
  {
    value: "$12.6B",
    label: "European sovereign cloud (IaaS)",
    line: "Spending in 2026, up 83% in a year.",
    sources: ["gartner-sovereign"],
  },
  {
    value: "774M",
    label: "Crypto owners",
    line: "The people RootWallet is built for.",
    sources: ["cryptocom"],
  },
];
