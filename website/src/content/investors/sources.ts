/**
 * Every figure on the investor page cites one of these. Numbers are shown in
 * this order ([1], [2], …); investors.test.ts fails the build if a figure
 * cites an id that is missing here or a source is never cited.
 * All read on 2026-09-24 unless the label says otherwise.
 */
export interface Source {
  id: string;
  label: string;
  url: string;
}

export const SOURCES: Source[] = [
  { id: "synergy-2025", label: "Synergy Research, cloud market full-year 2025 (Feb 2026)", url: "https://www.srgresearch.com/articles/genai-helps-drive-quarterly-cloud-revenues-to-119-billion-as-growth-rate-jumped-yet-again-in-q4" },
  { id: "synergy-q2-2026", label: "Synergy Research, Q2 2026 cloud market (Jul 2026)", url: "https://www.srgresearch.com/articles/q2-cloud-market-passes-143-billion-highest-growth-rate-in-eight-years" },
  { id: "gartner-sovereign", label: "Gartner via Computerworld, European sovereign cloud spending (Feb 2026)", url: "https://www.computerworld.com/article/4129552/gartner-european-spending-on-sovereign-cloud-iaas-to-nearly-double-in-2026.html" },
  { id: "gartner-cio", label: "Gartner, Western European CIOs and local cloud (Nov 2025)", url: "https://www.gartner.com/en/newsroom/press-releases/2025-11-12-gartner-survey-reveals-geopolitics-will-drive-61-percent-of-cios-and-information-technology-leaders-in-western-europe-to-increase-reliance-on-local-cloud-providers" },
  { id: "data-act", label: "Lindahl, EU Data Act cloud switching rules", url: "https://www.lindahl.se/en/latest-news/knowledge/new-requirements-for-cloud-portability-in-the-eu-data-act-practical-implications-for-cloud-service-providers/" },
  { id: "aws-oct-2025", label: "CyberCube, AWS us-east-1 outage impact (Oct 2025)", url: "https://www.cybcube.com/news/insurance-loss-estimate-for-aws-amazonk-outage" },
  { id: "cloudflare-nov-2025", label: "Cloudflare, 18 November 2025 outage post-mortem", url: "https://blog.cloudflare.com/18-november-2025-outage/" },
  { id: "railway-suspended", label: "The Register, Google Cloud suspends Railway (May 2026)", url: "https://www.theregister.com/off-prem/2026/05/20/google-cloud-suspended-major-customer-railwaycom-without-cause-causing-outage/5243111" },
  { id: "aws-may-2026", label: "IT Pro, AWS outage explained (May 2026)", url: "https://www.itpro.com/infrastructure/aws-outage-explained-may-2026-data-center-overheating" },
  { id: "cloudflare-fy", label: "Cloudflare, Q2 2026 results and FY2026 guidance (Aug 2026)", url: "https://www.cloudflare.com/press/press-releases/2026/cloudflare-announces-second-quarter-2026-financial-results/" },
  { id: "digitalocean-fy", label: "DigitalOcean, FY2025 results (Feb 2026)", url: "https://www.businesswire.com/news/home/20260224714253/en/DigitalOcean-Announces-Fourth-Quarter-and-Fiscal-Year-2025-Financial-Results" },
  { id: "vercel-arr", label: "Sacra, Vercel revenue and $9.3B valuation (2026)", url: "https://sacra.com/c/vercel/" },
  { id: "supabase-arr", label: "Sacra, Supabase revenue estimate (Jun 2026)", url: "https://sacra.com/research/supabase-170m-year-growing-221-yoy/" },
  { id: "supabase-round", label: "CNBC, Supabase raises at $10.5B (Jun 2026)", url: "https://www.cnbc.com/2026/06/04/database-startup-supabase-raises-500-million-10point5-billion-valuation.html" },
  { id: "railway-round", label: "Railway, Series B announcement (Jan 2026)", url: "https://blog.railway.com/p/series-b" },
  { id: "render-round", label: "Render, Series C extension at $1.5B (Feb 2026)", url: "https://render.com/blog/series-c-extension" },
  { id: "railway-pricing", label: "Railway, pricing", url: "https://railway.com/pricing" },
  { id: "supabase-pricing", label: "Supabase, pricing", url: "https://supabase.com/pricing" },
  { id: "cryptocom", label: "Crypto.com, Market Sizing Report H1 2026", url: "https://crypto.com/en/research/crypto-market-sizing-report-h1-2026" },
  { id: "a16z", label: "a16z crypto, State of Crypto 2025", url: "https://a16zcrypto.com/posts/article/state-of-crypto-report-2025/" },
  { id: "1password", label: "1Password, passes $400M ARR (Nov 2025)", url: "https://1password.com/press/2025/nov/1password-strengthens-leadership-amid-growth-milestone" },
  { id: "defillama-metamask", label: "DefiLlama, MetaMask fees", url: "https://defillama.com/protocol/metamask" },
  { id: "defillama-phantom", label: "DefiLlama, Phantom fees", url: "https://defillama.com/protocol/phantom" },
  { id: "metamask", label: "Bitcoin Magazine, MetaMask 30M monthly users (Dec 2025)", url: "https://bitcoinmagazine.com/business/metamask-launches-native-bitcoin-integration-for-30-million-active-users" },
  { id: "phantom", label: "Phantom, Series C announcement (Jan 2025)", url: "https://phantom.com/learn/blog/phantom-series-c" },
  { id: "exodus", label: "Exodus, FY2025 results (Mar 2026)", url: "https://www.exodus.com/investors/news-events/press-releases/detail/99/exodus-reports-fourth-quarter-2025-results-with-record-full-year-revenue" },
  { id: "dynamic", label: "Calcalist, Fireblocks acquires Dynamic for ~$90M (Oct 2025)", url: "https://www.calcalistech.com/ctechnews/article/s1zemqprel" },
  { id: "privy", label: "CoinDesk, Stripe to acquire Privy (Jun 2025)", url: "https://www.coindesk.com/business/2025/06/11/stripe-to-acquire-crypto-wallet-startup-privy-in-bid-to-expand-web3-capabilities" },
  { id: "fx", label: "Trading Economics, euro to dollar rate (24 Sep 2026)", url: "https://tradingeconomics.com/euro-area/currency" },
  { id: "eic", label: "European Innovation Council, 2026 work programme", url: "https://eic.ec.europa.eu/eic-funding-opportunities/eic-2026-work-programme_en" },
  { id: "innosuisse", label: "Innosuisse, start-up innovation projects", url: "https://www.innosuisse.admin.ch/en/start-up-innovation-projects" },
];

const INDEX = new Map(SOURCES.map((s, i) => [s.id, i + 1]));

/** The source's label, for screen readers. */
export function sourceLabel(id: string): string {
  return SOURCES[sourceNumber(id) - 1].label;
}

/** The [n] a source is shown as. Throws on an unknown id: a typo must not ship. */
export function sourceNumber(id: string): number {
  const n = INDEX.get(id);
  if (!n) throw new Error(`investors: unknown source "${id}"`);
  return n;
}
