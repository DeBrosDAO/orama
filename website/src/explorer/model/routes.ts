/** Every explorer URL is built here, so a route change is one edit. */
export const EXPLORER_BASE = "/explorer";

export const explorerPaths = {
  home: EXPLORER_BASE,
  validators: `${EXPLORER_BASE}/validators`,
  tx: (hash: string) => `${EXPLORER_BASE}/tx/${encodeURIComponent(hash)}`,
  wallet: (address: string) => `${EXPLORER_BASE}/wallet/${encodeURIComponent(address)}`,
  block: (height: number) => `${EXPLORER_BASE}/block/${height}`,
} as const;
