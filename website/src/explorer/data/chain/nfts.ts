import { parseAssetId } from "../../model/lookup";
import type { Collection, NftRecord, TreeInfo, TreeSnapshot } from "../../model/nfts";
import type { KeyPage, KeyQuery } from "../../model/paging";
import { ChainNotFound, ChainReadError, chainPath, withQuery } from "./client";
import type { ChainClient } from "./client";
import { INDEX_MAX_LIMIT } from "./aggregates";
import { lookup } from "./module-query";
import { ASSET_CURSOR, nextCursor } from "./paging";
import { clean } from "./text";
import { account, arr, digits, hash64, optArr, optStr, optUint, rec, str, uint } from "./wire";
import type { Rec } from "./wire";

const COLLECTION = "orama.cnft.v1.Query/Collection";
const TREE = "orama.cnft.v1.Query/Tree";
const SNAPSHOTS = "orama.cnft.v1.Query/Snapshots";

const MAX_NAME = 64;
const MAX_CID = 128;
const STATES = new Set(["compressed", "decompressed", "burned"]);

function malformed(what: string): ChainReadError {
  return new ChainReadError(`The chain sent a malformed ${what}.`);
}

export function readCollection(raw: unknown): Collection {
  const c = rec(raw, "collection");
  return {
    id: uint(c.id, "collection id"),
    creator: account(c.creator, "collection creator"),
    royaltyBps: optUint(c.royalty_bps, "collection royalty"),
    name: clean(optStr(c.name, "collection name", MAX_NAME * 4), MAX_NAME),
  };
}

/** One collection by id; null when x/cnft has none. */
export async function loadCollection(client: ChainClient, id: number): Promise<Collection | null> {
  if (!Number.isSafeInteger(id) || id < 1) return null;
  const answer = await lookup(client, COLLECTION, { id });
  if (answer === null) return null;
  const collection = readCollection(rec(answer, "collection").collection);
  if (collection.id !== id) throw malformed("collection (another id than asked for)");
  return collection;
}

/** One tree by id; null when x/cnft has none. */
export async function loadTree(client: ChainClient, id: number): Promise<TreeInfo | null> {
  if (!Number.isSafeInteger(id) || id < 1) return null;
  const answer = await lookup(client, TREE, { id });
  if (answer === null) return null;
  const t = rec(rec(answer, "tree").tree, "tree");
  return {
    id: uint(t.id, "tree id"),
    creator: account(t.creator, "tree creator"),
    collectionId: uint(t.collection_id, "tree collection"),
    depth: uint(t.depth, "tree depth"),
    bufferSize: uint(t.buffer_size, "tree buffer"),
    canopyDepth: optUint(t.canopy_depth, "tree canopy"),
    sequence: digits(t.sequence ?? "0", "tree sequence"),
    nextLeaf: digits(rec(t.rightmost, "tree rightmost leaf").index ?? "0", "tree rightmost leaf"),
  };
}

/** The content ids recorded for a tree, oldest first. */
export async function loadSnapshots(client: ChainClient, treeId: number): Promise<TreeSnapshot[]> {
  const answer = await lookup(client, SNAPSHOTS, { tree_id: treeId });
  if (answer === null) return [];
  return optArr(rec(answer, "tree snapshots").snapshots, "tree snapshots").map((raw) => {
    const s = rec(raw, "tree snapshot");
    return { id: uint(s.id, "snapshot id"), cid: clean(str(s.cid, "snapshot content id", MAX_CID * 4), MAX_CID), sequence: digits(s.sequence, "snapshot sequence") };
  });
}

function groupValue(groups: unknown[], key: string): string | null {
  const group = groups.map((g) => rec(g, "asset group")).find((g) => g.group_key === key);
  return group === undefined ? null : str(group.group_value, "asset group", 32);
}

/** One asset record as the indexer writes it (a DAS-style asset). */
export function readNftRecord(raw: unknown): NftRecord {
  const a = rec(raw, "asset");
  const id = parseAssetId(str(a.id, "asset id", 64));
  if (id === null) throw malformed("asset id");
  const own = rec(a.ownership, "asset ownership");
  const compression: Rec = rec(a.compression, "asset compression");
  const state = str(own.state, "asset state", 16);
  if (!STATES.has(state)) throw malformed("asset state");
  const collection = groupValue(arr(a.grouping, "asset groups"), "collection");
  const delegate = optStr(own.delegate, "asset delegate", 120);
  const update = rec(a.last_update, "asset update");
  return {
    id,
    state: state as NftRecord["state"],
    treeId: uint(compression.tree, "asset tree"),
    leafIndex: uint(compression.leaf_id, "asset leaf"),
    collectionId: Number(digits(collection ?? "0", "asset collection")),
    owner: account(own.owner, "asset owner"),
    delegate: delegate === "" ? null : account(delegate, "asset delegate"),
    metadataCid: clean(optStr(rec(a.content, "asset content").metadata_cid, "asset metadata id", MAX_CID * 4), MAX_CID),
    nonce: digits(compression.nonce ?? "0", "asset nonce"),
    updatedHeight: uint(update.height, "asset update height"),
    updatedTx: hash64(update.tx, "asset update transaction"),
  };
}

/** Every record of an asset id, or null when the index holds none (an id that was never minted, or minted before the index began). */
export async function loadAsset(client: ChainClient, id: string): Promise<NftRecord[] | null> {
  const asset = parseAssetId(id);
  if (asset === null) return null;
  try {
    const body = rec(await client.get(chainPath("index", "cnft", "assets", asset)), "asset");
    return arr(body.records, "asset records").map(readNftRecord);
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
}

/**
 * The assets an address holds, in asset id order, from the index: the first page by number, each next
 * one by the cursor the page before it named (`query.key`), so a deep page costs what the first does.
 */
export async function loadOwnerAssets(client: ChainClient, owner: string, query: KeyQuery): Promise<KeyPage<NftRecord>> {
  const limit = Math.min(INDEX_MAX_LIMIT, Math.max(1, Math.floor(query.limit)));
  const where = query.key === null ? { page: 1, limit } : { cursor: query.key, limit };
  const answer = rec(await client.get(withQuery(chainPath("index", "cnft", "owners", owner, "assets"), where)), "owner assets");
  const items = optArr(answer.assets, "owner assets").map(readNftRecord);
  return { items, nextKey: nextCursor(answer, items.length, limit, ASSET_CURSOR) };
}
