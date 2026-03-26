/**
 * Centralized fundraise constants — ALL prices in BTC.
 * Update numbers HERE — all pages pull from this single source.
 */

/* ── Token ── */
export const TOKEN_PRICE_BTC = 0;
export const TOKEN_LAUNCH_PRICE_BTC = 0;
export const TOTAL_TOKENS = 0;

/* ── Licenses ── */
export const TOTAL_LICENSES = 0;
export const LICENSE_PRICE_BTC = 0;

/* ── Fundraise ── */
export const FUNDRAISE_TARGET_BTC = 0;

/* ── Treasury ── */
export const TREASURY_BTC = "";

/* ── Current stats ── */
export const CURRENT_STATS = {
  tokens_sold: 0,
  tokens_remaining: 0,
  token_raised_btc: 0,
  licenses_sold: 0,
  licenses_left: 0,
  license_raised_btc: 0,
  total_raised_btc: 0,
  whitelist_count: 0,
} as const;

/**
 * Format BTC amount for display.
 * Shows up to 6 decimal places, trims trailing zeros.
 */
export function formatBTC(btc: number): string {
  if (btc >= 1) return btc.toFixed(2);
  if (btc >= 0.01) return btc.toFixed(4);
  return btc.toFixed(6).replace(/0+$/, "").replace(/\.$/, "");
}
