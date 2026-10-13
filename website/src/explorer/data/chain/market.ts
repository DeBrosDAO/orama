import type { Bid, Listing } from "../../model/market";
import { ChainReadError } from "./client";
import type { ChainClient } from "./client";
import { base64Bytes, toHex } from "./bytes";
import { lookup } from "./module-query";
import { account, digits, optStr, optUint, rec, uint } from "./wire";

const LISTING = "orama.market.v1.Query/Listing";
const BID = "orama.market.v1.Query/Bid";
const ASSET_ID_BYTES = 32;

function malformed(what: string): ChainReadError {
  return new ChainReadError(`The chain sent a malformed ${what}.`);
}

export function readListing(raw: unknown): Listing {
  const l = rec(raw, "listing");
  const assetId = base64Bytes(l.asset_id, "listing asset id", ASSET_ID_BYTES);
  if (assetId.length !== ASSET_ID_BYTES) throw malformed("listing asset id");
  const royaltyCreator = optStr(l.royalty_creator, "listing royalty creator", 120);
  return {
    id: uint(l.id, "listing id"),
    seller: account(l.seller, "listing seller"),
    treeId: uint(l.tree_id, "listing tree"),
    leafIndex: optUint(l.leaf_index, "listing leaf"),
    assetId: toHex(assetId),
    price: digits(l.price, "listing price"),
    royaltyCreator: royaltyCreator === "" ? null : account(royaltyCreator, "listing royalty creator"),
    royaltyBps: optUint(l.royalty_bps, "listing royalty"),
    collectionId: optUint(l.collection_id, "listing collection"),
  };
}

/** One listing by id; null when the market has none: it was never made, or it was cancelled or settled. */
export async function loadListing(client: ChainClient, id: number): Promise<Listing | null> {
  if (!Number.isSafeInteger(id) || id < 1) return null;
  const answer = await lookup(client, LISTING, { id });
  if (answer === null) return null;
  const listing = readListing(rec(answer, "listing").listing);
  if (listing.id !== id) throw malformed("listing (another id than asked for)");
  return listing;
}

/** One bid on a listing; null when there is none: it was never made, or it was cancelled, refunded or accepted. */
export async function loadBid(client: ChainClient, listingId: number, bidId: number): Promise<Bid | null> {
  if (![listingId, bidId].every((n) => Number.isSafeInteger(n) && n >= 1)) return null;
  const answer = await lookup(client, BID, { listing_id: listingId, bid_id: bidId });
  if (answer === null) return null;
  const b = rec(rec(answer, "bid").bid, "bid");
  const bid = {
    id: uint(b.id, "bid id"),
    listingId: uint(b.listing_id, "bid listing"),
    bidder: account(b.bidder, "bidder"),
    amount: digits(b.amount, "bid amount"),
  };
  if (bid.id !== bidId || bid.listingId !== listingId) throw malformed("bid (another bid than asked for)");
  return bid;
}
