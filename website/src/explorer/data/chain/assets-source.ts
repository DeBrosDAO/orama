import type { IdPage } from "../../model/paging";
import type { AssetsSource } from "../assets-source";
import type { ChainClient } from "./client";
import { loadCode, loadCodeContracts, loadCodes, loadContract, loadCreatedContracts, loadHistory, loadState } from "./contracts";
import { loadGovernance, loadProposal, loadVote } from "./governance";
import { loadBid, loadListing } from "./market";
import { idsDescending, newestId } from "./newest-id";
import { loadAsset, loadCollection, loadOwnerAssets, loadSnapshots, loadTree } from "./nfts";
import { loadShieldedActivity, loadShieldedActivityTotal, loadShieldedPool } from "./shielded";
import { loadFrozen, loadHolders, loadToken, loadTokenParams, loadTokens } from "./tokens";

/**
 * How long a read of an id-numbered record, and the newest id of its counter, are reused. Collections
 * and proposals are made in minutes and days, not in blocks, and a page of ten costs ten of the
 * gateway's module queries (120 a minute per reader), so paging back and forth must not ask again.
 */
const RECORD_TTL_MS = 15_000;
/** The reads kept at once; the memory of a long session is bounded by dropping them all when this many are held. */
const MAX_REMEMBERED = 200;

/** The explorer's reads of tokens, NFTs, contracts, the market, governance and the shielded pool, over the gateway's chain proxy. */
export function createAssetsSource(client: ChainClient, now: () => number = Date.now): AssetsSource {
  const remembered = new Map<string, { at: number; value: Promise<unknown> }>();

  /** `compute()` once per key for RECORD_TTL_MS; a read that failed is forgotten at once, so the next page asks again. */
  const memo = <T>(key: string, compute: () => Promise<T>): Promise<T> => {
    const hit = remembered.get(key);
    if (hit !== undefined && now() - hit.at <= RECORD_TTL_MS) return hit.value as Promise<T>;
    if (remembered.size >= MAX_REMEMBERED) remembered.clear();
    const value = compute();
    remembered.set(key, { at: now(), value });
    value.catch(() => {
      if (remembered.get(key)?.value === value) remembered.delete(key);
    });
    return value;
  };

  const newestFirst = async <T>(
    counter: string,
    read: (id: number) => Promise<T | null>,
    from: number | null,
    limit: number,
  ): Promise<IdPage<T>> => {
    const load = (id: number) => memo(`${counter}/${id}`, () => read(id));
    const top = await memo(`${counter}/newest`, () => newestId(async (id) => (await load(id)) !== null));
    const ids = idsDescending(from === null ? top : Math.min(from, top), limit);
    // A record is never a promise, so awaiting the loads leaves each as the T it was.
    const loaded = (await Promise.all(ids.map(load))) as (T | null)[];
    const last = ids[ids.length - 1];
    return {
      newest: top,
      items: loaded.filter((item): item is T => item !== null),
      next: last !== undefined && last > 1 ? last - 1 : null,
    };
  };

  return {
    getTokenParams: () => loadTokenParams(client),
    getTokens: (query) => loadTokens(client, query),
    getToken: (denom) => loadToken(client, denom),
    getTokenHolders: (denom, query) => loadHolders(client, denom, query),
    isFrozen: (denom, holder) => loadFrozen(client, denom, holder),

    getCollections: (from, limit) => newestFirst("collection", (id) => loadCollection(client, id), from, limit),
    getCollection: (id) => loadCollection(client, id),
    getTree: (id) => loadTree(client, id),
    getTreeSnapshots: (id) => loadSnapshots(client, id),
    getNftRecords: (assetId) => loadAsset(client, assetId),
    getOwnedNfts: (owner, query) => loadOwnerAssets(client, owner, query),

    getCodes: (query) => loadCodes(client, query),
    getCode: (codeId) => loadCode(client, codeId),
    getCodeContracts: (codeId, query) => loadCodeContracts(client, codeId, query),
    getContract: (address) => loadContract(client, address),
    getContractHistory: (address, query) => loadHistory(client, address, query),
    getContractState: (address, query) => loadState(client, address, query),
    getCreatedContracts: (creator, query) => loadCreatedContracts(client, creator, query),

    getListing: (id) => loadListing(client, id),
    getBid: (listingId, bidId) => loadBid(client, listingId, bidId),

    getGovernance: () => loadGovernance(client),
    getProposals: (from, limit) => newestFirst("proposal", (id) => loadProposal(client, id), from, limit),
    getProposal: (id) => loadProposal(client, id),
    getVote: (proposalId, voter) => loadVote(client, proposalId, voter),

    getShieldedPool: () => loadShieldedPool(client),
    getShieldedActivity: (query) => loadShieldedActivity(client, query),
    getShieldedActivityTotal: () => loadShieldedActivityTotal(client),
  };
}
