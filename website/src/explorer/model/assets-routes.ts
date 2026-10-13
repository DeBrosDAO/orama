/**
 * The URLs of the explorer's pages for tokens, NFTs, contracts, the market, governance and the
 * shielded pool. They live apart from routes.ts, which wires them in (`ExplorerPaths.assets`).
 */

/** The first segment of each of these pages, which a network segment can therefore never be. */
export const ASSET_PAGE_KINDS = [
  "tokens",
  "token",
  "nfts",
  "nft",
  "collection",
  "contracts",
  "code",
  "contract",
  "market",
  "listing",
  "governance",
  "proposal",
  "shielded",
] as const;

export interface AssetPaths {
  tokens: string;
  /** `denom` is factory/<creator>/<subdenom>; its page is /token/<creator>/<subdenom>. */
  token: (denom: string) => string;
  nfts: string;
  /** The NFTs an address holds. */
  nftsOf: (owner: string) => string;
  /** `assetId` is the 64 hex characters of the asset id. */
  nft: (assetId: string) => string;
  collection: (id: number) => string;
  contracts: string;
  code: (id: number) => string;
  contract: (address: string) => string;
  market: string;
  listing: (id: number) => string;
  governance: string;
  proposal: (id: number) => string;
  shielded: string;
}

const FACTORY_SEGMENTS = 3;

/** The builders under `base` (the explorer root, or a network's), with `query` appended to every URL. */
export function assetPathsFor(base: string, query: string): AssetPaths {
  const at = (path: string) => `${base}/${path}${query}`;
  return {
    tokens: at("tokens"),
    token: (denom) => {
      const parts = denom.split("/");
      if (parts.length !== FACTORY_SEGMENTS) return at("tokens");
      return at(`token/${encodeURIComponent(parts[1] ?? "")}/${encodeURIComponent(parts[2] ?? "")}`);
    },
    nfts: at("nfts"),
    nftsOf: (owner) => at(`nfts/${encodeURIComponent(owner)}`),
    nft: (assetId) => at(`nft/${encodeURIComponent(assetId)}`),
    collection: (id) => at(`collection/${id}`),
    contracts: at("contracts"),
    code: (id) => at(`code/${id}`),
    contract: (address) => at(`contract/${encodeURIComponent(address)}`),
    market: at("market"),
    listing: (id) => at(`listing/${id}`),
    governance: at("governance"),
    proposal: (id) => at(`proposal/${id}`),
    shielded: at("shielded"),
  };
}
