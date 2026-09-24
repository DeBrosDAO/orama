/**
 * Donation addresses. Copied verbatim from the live donation page on
 * 2026-09-24. An address typo sends money nowhere, so wallets.test.ts pins
 * each one's format.
 */
export interface Wallet {
  id: "btc" | "xmr" | "eth" | "sol";
  chain: string;
  symbol: string;
  network: string;
  address: string;
}

export const WALLETS: Wallet[] = [
  {
    id: "btc",
    chain: "Bitcoin",
    symbol: "BTC",
    network: "Native SegWit",
    address: "bc1qufd0cewe54awrekqd6guryum9q6jsnyjckucxe",
  },
  {
    id: "xmr",
    chain: "Monero",
    symbol: "XMR",
    network: "Monero",
    address:
      "48U8L3wCjpCdnTAq9iAa4DMCchF56srB9GgdN2SdszTRT41ydaiqDwqH7ChEYrDshhLBTKMW6R8HP6az3JGPkbDhB9TtqMz",
  },
  {
    id: "eth",
    chain: "Ethereum",
    symbol: "ETH",
    network: "Ethereum mainnet",
    address: "0x61482a75c5c0bE7667096264f637aB3272d6f4d0",
  },
  {
    id: "sol",
    chain: "Solana",
    symbol: "SOL",
    network: "Solana",
    address: "9JEsuEeRpxrJyFVkx1RRXBTG7V6zTeApr33nGrBpcYt3",
  },
];
