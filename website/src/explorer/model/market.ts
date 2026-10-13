import type { Norama } from "./types";

/** An x/market buy-now offer for one compressed asset. */
export interface Listing {
  id: number;
  seller: string;
  treeId: number;
  leafIndex: number;
  /** 64 hex characters. */
  assetId: string;
  /** The price in norama. */
  price: Norama;
  /** The collection's creator, copied at listing time; null when none is named. */
  royaltyCreator: string | null;
  royaltyBps: number;
  collectionId: number;
}

/** An escrowed offer against a listing. */
export interface Bid {
  id: number;
  listingId: number;
  bidder: string;
  amount: Norama;
}
