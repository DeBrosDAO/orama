import type { Norama } from "./types";

/** Storage deals (x/storage) and the block archive (x/archive), as the chain keeps them. */

export const DEAL_CLASSES = ["private", "public_pin", "archive"] as const;
export type DealClass = (typeof DEAL_CLASSES)[number];

export const DEAL_CLASS_LABEL: Record<DealClass, string> = {
  private: "Private",
  public_pin: "Public pin",
  archive: "Archive",
};

export const DEAL_STATUSES = ["open", "active", "expired", "refunded"] as const;
export type DealStatus = (typeof DEAL_STATUSES)[number];

export const SLOT_STATUSES = ["unassigned", "assigned", "active", "evicted", "released"] as const;
export type SlotStatus = (typeof SLOT_STATUSES)[number];

export interface StorageDeal {
  id: number;
  class: DealClass;
  /** Written by the protocol itself rather than a user's message. */
  protocol: boolean;
  client: string;
  /** The operator that repairs for the client, or null when the deal names none. */
  repairDelegate: string | null;
  replicas: number;
  pricePerEpoch: Norama;
  durationEpochs: number;
  startEpoch: number;
  endEpoch: number;
  escrow: Norama;
  status: DealStatus;
  createdHeight: number;
}

export interface StorageParams {
  minDealBytes: string;
  dealFee: Norama;
  /** Consecutive misses that evict a replica. */
  missThreshold: number;
  /** The protocol makes an archive deal and a public pin every this many epochs; 0 turns the schedule off. */
  protocolEveryEpochs: number;
  protocolPieceBytes: string;
  protocolPricePerEpoch: Norama;
  protocolDurationEpochs: number;
}

export interface DealList {
  params: StorageParams;
  /** The newest deals first. */
  deals: StorageDeal[];
  /** True when the chain holds older deals than the ones read. */
  hasOlder: boolean;
}

export interface DealReplica {
  index: number;
  /** null while no provider has been assigned the replica. */
  nodeId: string | null;
  status: SlotStatus;
  consecutiveMisses: number;
  pieceBytes: string;
}

export interface SettlementQueue {
  /** Payments and penalties waiting for a block to apply them. */
  pending: number;
}

export type RangeState = "none" | "attesting" | "decided" | "archived";

export interface ArchiveRange {
  start: number;
  end: number;
  state: RangeState;
  /** The most distinct operators that attested one bundle of the range. */
  attesters: number;
  /** Active archive deals recorded for the range. */
  replicaDeals: number;
}

export interface ArchiveHealth {
  tip: number;
  lastArchivedHeight: number;
  /** CometBFT may delete blocks below this height; 0 or less means nothing may be pruned yet. */
  retainHeight: number;
  retentionWindowBlocks: number;
  rangeBlocks: number;
  /** How far the archive holds the retain height back from where the retention window alone would put it. */
  retainLag: number;
  /** Complete ranges, up to the tip, that are not archived yet. */
  unarchivedRanges: number;
  /** The oldest unarchived ranges, at most a handful. */
  oldestUnarchived: ArchiveRange[];
}

/** An archiver quorum and a replica count, fixed by the chain and not parameters (chain/x/archive/types/constants.go). */
export const ARCHIVE_QUORUM = { attesters: 3, replicaDeals: 3 } as const;

export function retainLag(tip: number, retainHeight: number, retentionWindowBlocks: number): number {
  const windowFloor = tip - retentionWindowBlocks;
  return windowFloor > 0 && retainHeight < windowFloor ? windowFloor - retainHeight : 0;
}

/** Complete canonical ranges between the last archived height and the tip. */
export function unarchivedRangeCount(tip: number, lastArchivedHeight: number, rangeBlocks: number): number {
  if (rangeBlocks <= 0) return 0;
  return Math.max(0, Math.floor(tip / rangeBlocks) - Math.floor(lastArchivedHeight / rangeBlocks));
}

/** The first `count` canonical ranges after the archived prefix that end at or below the tip. */
export function oldestUnarchivedBounds(tip: number, lastArchivedHeight: number, rangeBlocks: number, count: number): { start: number; end: number }[] {
  const total = unarchivedRangeCount(tip, lastArchivedHeight, rangeBlocks);
  const first = Math.floor(lastArchivedHeight / rangeBlocks);
  return Array.from({ length: Math.min(count, total) }, (_, i) => ({ start: (first + i) * rangeBlocks + 1, end: (first + i + 1) * rangeBlocks }));
}

export function dealCounts(deals: readonly StorageDeal[]): Record<DealClass, number> {
  const counts: Record<DealClass, number> = { private: 0, public_pin: 0, archive: 0 };
  for (const deal of deals) counts[deal.class] += 1;
  return counts;
}

/** True for a deal that still pays providers: open or active. */
export function isRunning(deal: StorageDeal): boolean {
  return deal.status === "open" || deal.status === "active";
}
