/**
 * Payment via RootWallet (BTC only).
 * Currently in "Coming Soon" state — RootWallet BTC support is under development.
 */

export const TREASURY_BTC = "";

export const PAYMENT_COMING_SOON = true;

export async function payWithBTC(_amountBTC: number): Promise<string> {
  throw new Error("BTC payments via RootWallet are coming soon. Stay tuned!");
}
