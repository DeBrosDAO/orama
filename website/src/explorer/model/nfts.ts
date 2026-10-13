const BASIS_POINTS_PER_PERCENT = 100;

/** One x/cnft collection: the royalty and name every asset minted into it carries. */
export interface Collection {
  id: number;
  creator: string;
  /** Royalty on a sale, in basis points. */
  royaltyBps: number;
  name: string;
}

/** One concurrent Merkle tree of x/cnft; its leaves are compressed assets. */
export interface TreeInfo {
  id: number;
  creator: string;
  collectionId: number;
  depth: number;
  bufferSize: number;
  canopyDepth: number;
  /** Changes made to the tree. */
  sequence: string;
  /** The next leaf the tree appends at: the leaves written so far, burned ones included. */
  nextLeaf: string;
}

/** A content id recorded for a tree. Recording one opens no storage deal. */
export interface TreeSnapshot {
  id: number;
  cid: string;
  sequence: string;
}

export type AssetState = "compressed" | "decompressed" | "burned";

/** One record of an asset at one tree location, from the chain indexer. */
export interface NftRecord {
  /** 64 hex characters. */
  id: string;
  state: AssetState;
  treeId: number;
  leafIndex: number;
  collectionId: number;
  owner: string;
  delegate: string | null;
  /** The content id of the asset's metadata. The chain stores only this; the explorer does not fetch the document. */
  metadataCid: string;
  nonce: string;
  updatedHeight: number;
  /** 64 hex characters: the transaction that last changed the record. */
  updatedTx: string;
}

/** Royalty basis points as a percentage, "2.5%". */
export function royaltyPercent(bps: number): string {
  return `${bps / BASIS_POINTS_PER_PERCENT}%`;
}

/** The number of leaves a tree of this depth can hold. */
export function treeCapacity(depth: number): bigint {
  return 1n << BigInt(depth);
}
