import { DEAL_CLASSES, DEAL_STATUSES, SLOT_STATUSES } from "../../model/storage";
import type { DealClass, DealList, DealReplica, DealStatus, SettlementQueue, SlotStatus, StorageDeal, StorageParams } from "../../model/storage";
import { ChainReadError, withQuery } from "./client";
import type { ChainClient } from "./client";
import { NODE_ID } from "./nodes";
import { listAll } from "./paging";
import { clean } from "./text";
import { account, bool, digits, enumName, rec, str, uint } from "./wire";

const DEALS = "query/orama.storage.v1.Query/Deals";
const STORAGE_PARAMS = "query/orama.storage.v1.Query/Params";
const SLOT = "query/orama.storage.v1.Query/Slot";
const QUEUE = "query/orama.storage.v1.Query/Queue";

/** The newest deals read: one page. The page says it is the newest and that older ones exist. */
const DEAL_PAGES = 1;
const MAX_TEXT = 128;
const CLASS_PREFIX = "DEAL_CLASS_";
const STATUS_PREFIX = "DEAL_STATUS_";
const SLOT_PREFIX = "SLOT_STATUS_";

export function readDeal(raw: unknown): StorageDeal {
  const d = rec(raw, "storage deal");
  const delegate = d.repair_delegate === undefined || d.repair_delegate === null ? "" : str(d.repair_delegate, "repair delegate", MAX_TEXT);
  return {
    id: uint(d.id, "deal id"),
    class: enumName<DealClass>(d.class, CLASS_PREFIX, DEAL_CLASSES, "deal class"),
    protocol: bool(d.protocol, "deal protocol flag"),
    client: account(d.client, "deal client"),
    repairDelegate: delegate === "" ? null : clean(delegate, MAX_TEXT),
    replicas: uint(d.replicas, "deal replicas"),
    pricePerEpoch: digits(d.price_per_epoch, "deal price"),
    durationEpochs: uint(d.duration_epochs, "deal duration"),
    startEpoch: uint(d.start_epoch, "deal start"),
    endEpoch: uint(d.end_epoch, "deal end"),
    escrow: digits(d.escrow, "deal escrow"),
    status: enumName<DealStatus>(d.status, STATUS_PREFIX, DEAL_STATUSES, "deal status"),
    createdHeight: uint(d.created_height, "deal height"),
  };
}

function readStorageParams(raw: unknown): StorageParams {
  const p = rec(rec(raw, "storage parameters").params, "storage parameters");
  return {
    minDealBytes: digits(p.min_deal_bytes, "minimum deal size"),
    dealFee: digits(p.deal_fee, "deal fee"),
    missThreshold: uint(p.miss_threshold, "miss threshold"),
    protocolEveryEpochs: uint(p.protocol_every_epochs, "protocol deal schedule"),
    protocolPieceBytes: digits(p.protocol_piece_bytes, "protocol deal size"),
    protocolPricePerEpoch: digits(p.protocol_price_per_epoch, "protocol deal price"),
    protocolDurationEpochs: uint(p.protocol_duration_epochs, "protocol deal duration"),
  };
}

/** The newest deals, with the module's parameters. */
export async function loadDeals(client: ChainClient): Promise<DealList> {
  const [listed, params] = await Promise.all([
    listAll(client, DEALS, "deals", readDeal, { maxPages: DEAL_PAGES, reverse: true }),
    client.get(STORAGE_PARAMS),
  ]);
  return { params: readStorageParams(params), deals: listed.items, hasOlder: listed.more };
}

export async function loadSettlementQueue(client: ChainClient): Promise<SettlementQueue> {
  return { pending: uint(rec(await client.get(QUEUE), "settlement queue").pending, "settlement queue") };
}

function readReplica(index: number, raw: unknown): DealReplica {
  const s = rec(rec(raw, "deal replica").slot, "deal replica");
  const nodeId = str(s.node_id, "replica node", 64);
  if (nodeId !== "" && !NODE_ID.test(nodeId)) throw new ChainReadError("The chain sent a malformed replica node.");
  return {
    index,
    nodeId: nodeId === "" ? null : nodeId,
    status: enumName<SlotStatus>(s.status, SLOT_PREFIX, SLOT_STATUSES, "replica status"),
    consecutiveMisses: uint(s.consecutive_misses, "replica misses"),
    pieceBytes: digits(s.piece_bytes, "replica size"),
  };
}

/** The most replicas of one deal read: each is a query of its own. */
export const MAX_REPLICAS_READ = 12;

/** A deal's replicas, one query each. The chain writes every slot when it makes the deal, so a slot it cannot answer is a failure. */
export async function loadReplicas(client: ChainClient, dealId: number, replicas: number): Promise<DealReplica[]> {
  if (!Number.isSafeInteger(dealId) || dealId < 1) return [];
  return Promise.all(
    Array.from({ length: Math.min(replicas, MAX_REPLICAS_READ) }, async (_, index) => {
      const path = withQuery(SLOT, { json: JSON.stringify({ deal_id: String(dealId), slot: index }) });
      return readReplica(index, await client.get(path));
    }),
  );
}
