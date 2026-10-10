/**
 * Competitor grids. Cells are true / false or a short qualifier. Public
 * product capabilities as of September 2026.
 */
export type Cell = boolean | string;

export interface Grid {
  columns: string[];
  rows: { label: string; cells: Cell[] }[];
  /** Column index of our own product, highlighted. */
  ours: number;
}

export const CLOUD_COMPETITION: Grid = {
  columns: ["Supabase", "Vercel", "Railway", "Akash", "ICP", "Orama"],
  ours: 5,
  rows: [
    { label: "Database, functions, storage", cells: [true, "Partial", true, false, true, true] },
    { label: "Calls and push built in", cells: [false, false, false, false, false, true] },
    { label: "No single company can switch it off", cells: [false, false, false, true, true, "Built for it"] },
    { label: "Standard code (Node, Go, Next.js)", cells: [true, true, true, true, "Own runtime", true] },
    { label: "No token needed", cells: [true, true, true, "Card via Console", false, true] },
  ],
};

export const WALLET_COMPETITION: Grid = {
  columns: ["MetaMask", "Phantom", "1Password", "Proton", "Ledger", "RootWallet"],
  ours: 5,
  rows: [
    { label: "Crypto wallet", cells: [true, true, false, "BTC only", true, true] },
    { label: "Password manager", cells: [false, false, true, true, false, true] },
    { label: "2FA codes", cells: [false, false, true, true, false, true] },
    { label: "SSH keys", cells: [false, false, true, true, "Limited", true] },
    { label: "No account needed", cells: [true, true, false, false, true, true] },
    { label: "Nothing stored in the cloud", cells: [true, "Optional backup", false, false, true, true] },
    { label: "All of it in one app", cells: [false, false, false, false, false, true] },
  ],
};

/** The funded companies that prove the category, and what investors paid. */
export const COMPARABLES = [
  { what: "Supabase", detail: "Backend for developers · $10.5B valuation · Jun 2026", source: "supabase-round" },
  { what: "Vercel", detail: "Frontend cloud · $9.3B valuation · Sep 2025", source: "vercel-arr" },
  { what: "Render", detail: "Developer cloud · $1.5B valuation · Feb 2026", source: "render-round" },
  { what: "Railway", detail: "Developer cloud · $100M Series B · Jan 2026", source: "railway-round" },
  { what: "Phantom", detail: "Self-custody wallet · $3B valuation · Jan 2025", source: "phantom" },
  { what: "Fireblocks → Dynamic", detail: "Wallet platform acquired · ~$90M · Oct 2025", source: "dynamic" },
  { what: "Stripe → Privy", detail: "Embedded wallets acquired · Jun 2025", source: "privy" },
] as const;
