import type { CodeInfo, ContractInfo, HistoryEntry, StateEntry } from "../model/contracts";
import type { Governance, Proposal, Vote } from "../model/governance";
import type { Bid, Listing } from "../model/market";
import type { Collection, NftRecord, TreeInfo, TreeSnapshot } from "../model/nfts";
import type { IdPage, KeyPage, KeyQuery } from "../model/paging";
import type { ShieldedActivity, ShieldedPool } from "../model/shielded";
import type { TokenDetail, TokenHolder, TokenParams, TokenRow } from "../model/tokens";

/**
 * What the explorer reads of the chain's tokens, NFTs, contracts, market, governance and shielded
 * pool. It follows the contract of ExplorerDataSource: a lookup for something that does not exist
 * resolves to null, a real failure rejects with an Error a reader can be shown, and a list is
 * paged by the key the chain gives (KeyPage), by id (IdPage) or by number (the indexer's lists).
 */
export interface AssetsSource {
  getTokenParams(): Promise<TokenParams>;
  /** The tokens x/token has issued, in denom order. */
  getTokens(query: KeyQuery): Promise<KeyPage<TokenRow>>;
  /** `denom` is factory/<creator>/<subdenom>; null when x/token has no such token. */
  getToken(denom: string): Promise<TokenDetail | null>;
  getTokenHolders(denom: string, query: KeyQuery): Promise<KeyPage<TokenHolder>>;
  isFrozen(denom: string, account: string): Promise<boolean>;

  /** Collections, newest first, `limit` of them starting at id `from` (the newest when null). */
  getCollections(from: number | null, limit: number): Promise<IdPage<Collection>>;
  getCollection(id: number): Promise<Collection | null>;
  getTree(id: number): Promise<TreeInfo | null>;
  getTreeSnapshots(id: number): Promise<TreeSnapshot[]>;
  /** Every record of an asset id from the chain indexer; null when the index holds none. Rejects when the node has no indexer. */
  getNftRecords(assetId: string): Promise<NftRecord[] | null>;
  /** The assets an address holds in asset id order, from the chain indexer; `key` is the indexer cursor of the page before. */
  getOwnedNfts(owner: string, query: KeyQuery): Promise<KeyPage<NftRecord>>;

  getCodes(query: KeyQuery): Promise<KeyPage<CodeInfo>>;
  getCode(codeId: number): Promise<CodeInfo | null>;
  getCodeContracts(codeId: number, query: KeyQuery): Promise<KeyPage<string>>;
  /** null when the address is not a contract. */
  getContract(address: string): Promise<ContractInfo | null>;
  getContractHistory(address: string, query: KeyQuery): Promise<KeyPage<HistoryEntry>>;
  getContractState(address: string, query: KeyQuery): Promise<KeyPage<StateEntry>>;
  getCreatedContracts(creator: string, query: KeyQuery): Promise<KeyPage<string>>;

  /** null when the market has none: never made, cancelled or settled. */
  getListing(id: number): Promise<Listing | null>;
  getBid(listingId: number, bidId: number): Promise<Bid | null>;

  getGovernance(): Promise<Governance>;
  /** Proposals, newest first, `limit` of them starting at id `from` (the newest when null). */
  getProposals(from: number | null, limit: number): Promise<IdPage<Proposal>>;
  getProposal(id: number): Promise<Proposal | null>;
  getVote(proposalId: number, voter: string): Promise<Vote | null>;

  getShieldedPool(): Promise<ShieldedPool>;
  /** The transactions that touched the pool, newest first, from the chain indexer. */
  getShieldedActivity(query: KeyQuery): Promise<ShieldedActivity>;
  /** Transactions that named the pool's account since the index began; null when the index has none for it. */
  getShieldedActivityTotal(): Promise<number | null>;
}
