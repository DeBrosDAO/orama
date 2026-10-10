import { NetworkError, NotFoundError, SDKError } from "../errors";
import { BASE_DENOM, printable } from "./format";
import type { AnyMsg } from "./msg";
import type { OramaSigner } from "./signer";
import { signTx, txHashOf, type SignedTx } from "./tx";
import { transfer, withdrawEarnings, type TransferOptions, type TransferRequest, type TransferResult } from "./transfer";

/** Where the chain is read from. Each read names the base it uses. */
export interface ChainClientConfig {
  /** The gateway root, for the read-only /v1/chain/ proxy: status, blocks, transactions, validators, the indexer, module queries. */
  gatewayURL?: string;
  /**
   * A node's Cosmos REST API root, for example http://127.0.0.1:31003. Needed to
   * read an account (number, sequence), bank balances, and to broadcast: the
   * gateway proxy serves none of those.
   */
  restURL?: string;
  /** Replaces the platform's fetch. */
  fetch?: typeof fetch;
  /** Milliseconds before a request is abandoned. Default 15000. */
  timeoutMs?: number;
}

/** A chain account as the signature needs it. */
export interface ChainAccount {
  accountNumber: bigint;
  sequence: bigint;
  /** The 33-byte public key, once the chain has seen the account sign. */
  publicKey?: Uint8Array;
}

/** What a broadcast returns. A non-zero code is thrown, never returned. */
export interface BroadcastResult {
  txHash: string;
  code: 0;
  rawLog: string;
}

/** What POST /v1/chain/simulate answers for a transaction the chain would run. */
export interface SimulateResult {
  /** The gas limit the transaction declares. The chain's simulation itself runs with no limit. */
  gasWanted: bigint;
  gasUsed: bigint;
  /** The fee at the chain's current base fee for the gas used. */
  fee: { denom: string; amount: string };
  /** Norama per unit of gas, so a caller that pads the gas limit can price the padded limit. */
  baseFee: string;
}

/** What POST /v1/chain/broadcast answers for a transaction the chain's mempool took. */
export interface GatewayBroadcastResult {
  /** Upper-case hex SHA-256 of the transaction; read it with `tx(hash)` until it is in a block. */
  txHash: string;
  code: 0;
  log: string;
}

/**
 * A transaction the chain refused on simulate or broadcast. `chainCode` and `codespace` are the
 * chain's; `log` is the gateway's sanitised copy of its reason. `txHash` is set on a broadcast.
 */
export class ChainTxRefusedError extends SDKError {
  readonly chainCode: number;
  readonly codespace: string;
  readonly log: string;
  readonly txHash?: string;

  constructor(body: { code: number; codespace?: string; log?: string; tx_hash?: string }) {
    const log = printable(body.log ?? "");
    super(
      `the chain rejected the transaction (code ${body.code}): ${log.slice(0, 200)}`,
      422,
      "CHAIN_TX_REJECTED",
      { code: body.code, codespace: body.codespace ?? "", log, txHash: body.tx_hash },
    );
    this.name = "ChainTxRefusedError";
    this.chainCode = body.code;
    this.codespace = body.codespace ?? "";
    this.log = log;
    this.txHash = body.tx_hash || undefined;
  }
}

/** A page of a listing: how many entries, and where to continue from. */
export interface WalletPageOptions {
  /** Entries per page, 1 to 100. Absent is the chain's default of 100. */
  limit?: number;
  /** The `pagination.next_key` of the previous page (base64). */
  key?: string;
}

export interface SignAndBroadcastOptions {
  chainId: string;
  gasLimit: bigint | number | string;
  feeNorama: bigint | number | string;
  memo?: string;
}

export interface PageOptions {
  page?: number;
  limit?: number;
}

/** What a module query answers: the decoded response, with the proto field names. uint64 fields are decimal strings. */
export type ChainQueryResult = Record<string, unknown>;

export interface QueryOptions {
  /** Read at this height. Absent is the latest. */
  height?: number;
}

/** An unsigned 64-bit id as a number, bigint or decimal string. */
export type Uint64Like = number | bigint | string;

const DEFAULT_TIMEOUT_MS = 15_000;
const HEX_HASH = /^(0x)?[0-9a-fA-F]{64}$/;
const NODES = "orama.nodes.v1.Query";
const STORAGE = "orama.storage.v1.Query";
const FEES = "orama.fees.v1.Query";
const ARCHIVE = "orama.archive.v1.Query";
const RELAY = "orama.relay.v1.Query";
const BANK = "cosmos.bank.v1beta1.Query";
const AUTH = "cosmos.auth.v1beta1.Query";
const STAKING = "cosmos.staking.v1beta1.Query";
const DISTRIBUTION = "cosmos.distribution.v1beta1.Query";
const WASM = "cosmwasm.wasm.v1.Query";
const ORAMA_ADDRESS = /^orama1[02-9ac-hj-np-z]{6,90}$/;
const VALOPER_ADDRESS = /^oramavaloper1[02-9ac-hj-np-z]{6,90}$/;
const DENOM = /^[a-zA-Z][a-zA-Z0-9/:._-]{2,127}$/;
const BASE64 = /^[A-Za-z0-9+/_-]*={0,2}$/;
/** The most entries the gateway serves in one page of a wallet listing. */
const WALLET_MAX_PAGE_LIMIT = 100;
/** The largest transaction the gateway takes: CometBFT's default mempool max_tx_bytes. */
const TX_MAX_BYTES = 1 << 20;

function requireBase(base: string | undefined, name: string, use: string): string {
  if (!base) throw new Error(`${use} needs ${name} in the chain client config`);
  return base.replace(/\/+$/, "");
}

function assertAddress(address: string): string {
  if (!ORAMA_ADDRESS.test(address)) throw new Error(`"${address}" is not an orama address`);
  return address;
}

function assertValoper(address: string): string {
  if (!VALOPER_ADDRESS.test(address)) throw new Error(`"${address}" is not an oramavaloper address`);
  return address;
}

function assertDenom(denom: string): string {
  if (!DENOM.test(denom)) throw new Error(`"${denom}" is not a denomination`);
  return denom;
}

function pagination(options: WalletPageOptions): Record<string, unknown> {
  const page: Record<string, unknown> = {};
  if (options.limit !== undefined) {
    if (!Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > WALLET_MAX_PAGE_LIMIT) {
      throw new Error(`limit must be an integer from 1 to ${WALLET_MAX_PAGE_LIMIT}`);
    }
    page.limit = options.limit;
  }
  if (options.key !== undefined) {
    if (!options.key || options.key.length > 1024 || !BASE64.test(options.key)) throw new Error("key must be a pagination next_key (base64)");
    page.key = options.key;
  }
  return Object.keys(page).length === 0 ? {} : { pagination: page };
}

function assertTxBytes(txBytes: Uint8Array): Uint8Array {
  if (txBytes.length === 0 || txBytes.length > TX_MAX_BYTES) throw new Error(`a transaction is 1 to ${TX_MAX_BYTES} bytes`);
  return txBytes;
}

function assertHash(hash: string): string {
  if (!HEX_HASH.test(hash)) throw new Error("a transaction hash is 64 hex characters");
  return hash.replace(/^0x/, "").toLowerCase();
}

function assertUint(value: number, name: string): number {
  if (!Number.isSafeInteger(value) || value < 0) throw new Error(`${name} must be a non-negative integer`);
  return value;
}

function assertUint64(value: Uint64Like, name: string): string {
  const text = typeof value === "number" ? (Number.isSafeInteger(value) && value >= 0 ? String(value) : "") : String(value);
  if (!/^(0|[1-9][0-9]{0,19})$/.test(text) || BigInt(text) > 0xffff_ffff_ffff_ffffn) {
    throw new Error(`${name} must be an unsigned 64-bit integer`);
  }
  return text;
}

function assertText(value: string, name: string, max = 128): string {
  // Rejecting ASCII control characters is the point of this pattern.
  // eslint-disable-next-line no-control-regex
  if (!value || value.length > max || /[\u0000-\u001f]/.test(value)) throw new Error(`${name} must be 1 to ${max} printable characters`);
  return value;
}

function query(params: Record<string, string | number | undefined>): string {
  const pairs = Object.entries(params).filter(([, v]) => v !== undefined);
  if (pairs.length === 0) return "";
  return "?" + pairs.map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`).join("&");
}

/**
 * Reads the Orama chain and submits signed transactions.
 *
 * Reads through the gateway (`gatewayURL`) need no credential. The Orama
 * modules' own state (x/nodes, x/storage, x/fees, x/archive, x/relay) speaks
 * gRPC only and has no REST route; the gateway serves each module's Query
 * service at /v1/chain/query/<package.Service>/<Method>, which `moduleQuery`
 * and the typed reads below call.
 */
export class OramaChainClient {
  private readonly config: ChainClientConfig;
  private readonly fetchFn: typeof fetch;

  constructor(config: ChainClientConfig) {
    this.config = config;
    this.fetchFn = config.fetch ?? ((...args) => fetch(...args));
  }

  // ---- the gateway's /v1/chain/ proxy ----

  /** CometBFT status: network id, latest block, sync state. */
  async status(): Promise<unknown> {
    return this.gateway("status");
  }

  /** One block by height. */
  async block(height: number): Promise<unknown> {
    return this.gateway(`block${query({ height: assertUint(height, "height") })}`);
  }

  /** Block metas between two heights, at most 20. */
  async blocks(minHeight: number, maxHeight: number): Promise<unknown> {
    return this.gateway(
      `blocks${query({ min_height: assertUint(minHeight, "minHeight"), max_height: assertUint(maxHeight, "maxHeight") })}`,
    );
  }

  /** A transaction by its 32-byte hash, from CometBFT. */
  async tx(hash: string): Promise<unknown> {
    return this.gateway(`tx${query({ hash: assertHash(hash) })}`);
  }

  /** The validator set. */
  async validators(options: { page?: number; perPage?: number } = {}): Promise<unknown> {
    return this.gateway(`validators${query({ page: options.page, per_page: options.perPage })}`);
  }

  /** Total supply of norama. */
  async supply(): Promise<unknown> {
    return this.gateway("supply/norama");
  }

  /** The staking pool: bonded and not-bonded tokens. */
  async stakingPool(): Promise<unknown> {
    return this.gateway("staking/pool");
  }

  /** The indexer's status and the height it has reached. */
  async indexStatus(): Promise<unknown> {
    return this.gateway("index/status");
  }

  /** A block as the indexer holds it. */
  async indexBlock(height: number): Promise<unknown> {
    return this.gateway(`index/blocks/${assertUint(height, "height")}`);
  }

  /** A transaction as the indexer holds it. */
  async indexTx(hash: string): Promise<unknown> {
    return this.gateway(`index/txs/${assertHash(hash)}`);
  }

  /** An account's transactions, newest first. */
  async indexAccountTxs(address: string, options: PageOptions = {}): Promise<unknown> {
    return this.gateway(
      `index/accounts/${assertAddress(address)}/txs${query({ page: options.page, limit: options.limit })}`,
    );
  }

  /** A compressed NFT by its 32-byte asset id. */
  async cnftAsset(assetId: string): Promise<unknown> {
    return this.gateway(`index/cnft/assets/${assertHash(assetId)}`);
  }

  /** The compressed NFTs an address owns. */
  async cnftOwnerAssets(owner: string, options: PageOptions = {}): Promise<unknown> {
    return this.gateway(
      `index/cnft/owners/${assertAddress(owner)}/assets${query({ page: options.page, limit: options.limit })}`,
    );
  }

  // ---- Orama module queries, through /v1/chain/query/ ----

  /**
   * Runs one query of an Orama module's Query service, for example
   * `moduleQuery("orama.nodes.v1.Query", "Node", { node_id: "n-1" })`. The
   * request is JSON with the proto field names; the gateway refuses any service
   * or method it does not embed. A key that is not on chain is an SDKError with
   * httpStatus 404.
   */
  async moduleQuery(
    service: string,
    method: string,
    request: Record<string, unknown> = {},
    options: QueryOptions = {},
  ): Promise<ChainQueryResult> {
    if (!/^orama\.[a-z0-9_]+\.v[0-9]+\.Query$/.test(service)) throw new Error(`"${service}" is not an Orama Query service`);
    return this.runQuery(service, method, request, options);
  }

  private async runQuery(
    service: string,
    method: string,
    request: Record<string, unknown>,
    options: QueryOptions,
  ): Promise<ChainQueryResult> {
    if (!/^[A-Za-z][A-Za-z0-9]*$/.test(method)) throw new Error(`"${method}" is not a query method`);
    const json = Object.keys(request).length === 0 ? undefined : JSON.stringify(request);
    const height = options.height === undefined ? undefined : assertUint(options.height, "height");
    return (await this.gateway(`query/${service}/${method}${query({ json, height })}`)) as ChainQueryResult;
  }

  // ---- what a wallet reads, through /v1/chain/query/ (no node, no tunnel) ----

  /** One bank balance of an address. Default denomination norama. Earnings are a separate account. */
  async bankBalance(address: string, denom: string = BASE_DENOM, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(BANK, "Balance", { address: assertAddress(address), denom: assertDenom(denom) }, options);
  }

  /** Every bank balance of an address, a page at a time. */
  async bankAllBalances(address: string, page: WalletPageOptions = {}, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(BANK, "AllBalances", { address: assertAddress(address), ...pagination(page) }, options);
  }

  /** The balances of an address that are not locked, a page at a time. */
  async bankSpendableBalances(address: string, page: WalletPageOptions = {}, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(BANK, "SpendableBalances", { address: assertAddress(address), ...pagination(page) }, options);
  }

  /** An auth account. An address the chain has never seen is an SDKError with httpStatus 404. */
  async authAccount(address: string, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(AUTH, "Account", { address: assertAddress(address) }, options);
  }

  /** An auth account's address, public key, number and sequence. A new address is a 404. */
  async authAccountInfo(address: string, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(AUTH, "AccountInfo", { address: assertAddress(address) }, options);
  }

  /** What a delegator has staked with one validator. None is a 404. */
  async stakingDelegation(delegator: string, validator: string, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(STAKING, "Delegation", { delegator_addr: assertAddress(delegator), validator_addr: assertValoper(validator) }, options);
  }

  /** Every delegation of a delegator, a page at a time. */
  async stakingDelegatorDelegations(delegator: string, page: WalletPageOptions = {}, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(STAKING, "DelegatorDelegations", { delegator_addr: assertAddress(delegator), ...pagination(page) }, options);
  }

  /** A delegator's unbonding from one validator. None is a 404. */
  async stakingUnbondingDelegation(delegator: string, validator: string, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(STAKING, "UnbondingDelegation", { delegator_addr: assertAddress(delegator), validator_addr: assertValoper(validator) }, options);
  }

  /** Every unbonding of a delegator, a page at a time. */
  async stakingDelegatorUnbondingDelegations(delegator: string, page: WalletPageOptions = {}, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(STAKING, "DelegatorUnbondingDelegations", { delegator_addr: assertAddress(delegator), ...pagination(page) }, options);
  }

  /** One validator by its oramavaloper address. */
  async stakingValidator(validator: string, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(STAKING, "Validator", { validator_addr: assertValoper(validator) }, options);
  }

  /** The staking parameters. Also the way to tell that the gateway serves wallet queries. */
  async stakingParams(options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(STAKING, "Params", {}, options);
  }

  /** The rewards a delegator has accrued with one validator. */
  async distributionDelegationRewards(delegator: string, validator: string, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(DISTRIBUTION, "DelegationRewards", { delegator_address: assertAddress(delegator), validator_address: assertValoper(validator) }, options);
  }

  /** The rewards a delegator has accrued with every validator it delegates to. */
  async distributionDelegationTotalRewards(delegator: string, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(DISTRIBUTION, "DelegationTotalRewards", { delegator_address: assertAddress(delegator) }, options);
  }

  /** A contract's record. An address that is not a contract is an SDKError with httpStatus 404. */
  async contractInfo(address: string, options: QueryOptions = {}): Promise<ChainQueryResult> {
    return this.runQuery(WASM, "ContractInfo", { address: assertAddress(address) }, options);
  }

  /** Whether an address is a contract, so a wallet can tell a contract from a plain account before it signs a send. */
  async isContract(address: string, options: QueryOptions = {}): Promise<boolean> {
    try {
      await this.contractInfo(address, options);
      return true;
    } catch (err) {
      if (err instanceof NotFoundError) return false;
      throw err;
    }
  }

  // ---- simulate and broadcast through the gateway ----

  /**
   * Runs a signed transaction (TxRaw bytes) without keeping anything, and returns the gas it needs
   * and the fee at the current base fee. A transaction the chain would refuse is a
   * ChainTxRefusedError.
   */
  async simulateTx(txBytes: Uint8Array): Promise<SimulateResult> {
    const body = (await this.post("simulate", assertTxBytes(txBytes))) as {
      gas_wanted: number | string;
      gas_used: number | string;
      fee: { denom: string; amount: string };
      base_fee: string;
    };
    return {
      gasWanted: gatewayUint64(body.gas_wanted, "gas_wanted"),
      gasUsed: gatewayUint64(body.gas_used, "gas_used"),
      fee: body.fee,
      baseFee: body.base_fee,
    };
  }

  /**
   * Submits a signed transaction (TxRaw bytes) through the gateway. It answers when the chain has
   * checked it, not when it is in a block: read `tx(hash)` until it is. A transaction the chain
   * refuses is a ChainTxRefusedError; sending the same bytes again is one too (code 19), with the
   * hash to read.
   */
  async broadcastTx(txBytes: Uint8Array): Promise<GatewayBroadcastResult> {
    const body = (await this.post("broadcast", assertTxBytes(txBytes))) as { tx_hash: string; log: string };
    return { txHash: boundHash(body.tx_hash, txBytes), code: 0, log: body.log };
  }

  /** x/nodes parameters. */
  async nodesParams(options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(NODES, "Params", {}, options);
  }

  /** An x/nodes operator by account address. */
  async operator(address: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(NODES, "Operator", { address: assertAddress(address) }, options);
  }

  /** A registered node: operator, roles, bonds, endpoints, capacity, status. */
  async node(nodeId: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(NODES, "Node", { node_id: assertText(nodeId, "nodeId") }, options);
  }

  /** An x/nodes cluster. */
  async nodeCluster(clusterId: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(NODES, "Cluster", { cluster_id: assertText(clusterId, "clusterId") }, options);
  }

  /** A node's bond unbondings still in progress. */
  async nodeUnbondings(nodeId: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(NODES, "NodeUnbondings", { node_id: assertText(nodeId, "nodeId") }, options);
  }

  /** The node that holds an identification name, with the literal IPs of its endpoints. */
  async nodeByName(name: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(NODES, "NodeByName", { name: assertText(name, "name") }, options);
  }

  /** The identification name a node holds, with its deposit. */
  async nameOfNode(nodeId: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(NODES, "NameOfNode", { node_id: assertText(nodeId, "nodeId") }, options);
  }

  /** One page of every claimed node name, in name order. Pass the previous page's `next_key` to continue. */
  async nodeNames(pageKey?: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(NODES, "NodeNames", pageKey ? { pagination: { key: pageKey } } : {}, options);
  }

  /** x/storage parameters. */
  async storageParams(options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(STORAGE, "Params", {}, options);
  }

  /** A storage deal. */
  async deal(dealId: Uint64Like, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(STORAGE, "Deal", { deal_id: assertUint64(dealId, "dealId") }, options);
  }

  /** One replica slot of a deal: its provider, piece root and status. */
  async slot(dealId: Uint64Like, slot: number, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(
      STORAGE,
      "Slot",
      { deal_id: assertUint64(dealId, "dealId"), slot: assertUint(slot, "slot") },
      options,
    );
  }

  /** The deal authorization a granter gave a grantee. */
  async storageAuthorization(granter: string, grantee: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(
      STORAGE,
      "Authorization",
      { granter: assertAddress(granter), grantee: assertAddress(grantee) },
      options,
    );
  }

  /** The storage challenges of an epoch, for a node. */
  async storageChallenges(epoch: Uint64Like, nodeId: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(
      STORAGE,
      "Challenges",
      { epoch: assertUint64(epoch, "epoch"), node_id: assertText(nodeId, "nodeId") },
      options,
    );
  }

  /** What x/storage minted for an epoch. */
  async storageEpochMint(epoch: Uint64Like, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(STORAGE, "EpochMint", { epoch: assertUint64(epoch, "epoch") }, options);
  }

  /** The x/storage assignment queue: pending, head and tail. */
  async storageQueue(options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(STORAGE, "Queue", {}, options);
  }

  /** x/fees parameters. */
  async feesParams(options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(FEES, "Params", {}, options);
  }

  /** The current base fee. */
  async baseFee(options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(FEES, "BaseFee", {}, options);
  }

  /** The earnings balance x/fees holds for an address. It is not in the bank balance. */
  async earnings(address: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(FEES, "Earnings", { address: assertAddress(address) }, options);
  }

  /** An x/fees deposit by id. */
  async feesDeposit(id: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(FEES, "Deposit", { id: assertText(id, "id") }, options);
  }

  /** x/archive parameters. */
  async archiveParams(options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(ARCHIVE, "Params", {}, options);
  }

  /** The archived range record of a block range. */
  async archiveRange(startHeight: number, endHeight: number, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(
      ARCHIVE,
      "Range",
      { start_height: String(assertUint(startHeight, "startHeight")), end_height: String(assertUint(endHeight, "endHeight")) },
      options,
    );
  }

  /** The contiguous archived prefix: the height below which the chain lets nodes prune. */
  async lastArchivedHeight(options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(ARCHIVE, "LastArchivedHeight", {}, options);
  }

  /** The retain height the chain gives nodes, with the tip and the archived prefix. */
  async retainHeight(options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(ARCHIVE, "RetainHeight", {}, options);
  }

  /** x/relay parameters. */
  async relayParams(options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(RELAY, "Params", {}, options);
  }

  /** The accounts allowed to report relay measurements. */
  async relayReporters(options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(RELAY, "Reporters", {}, options);
  }

  /** A relay by the hex SHA-1 fingerprint of its RSA identity key. */
  async relay(rsaFingerprintHex: string, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(RELAY, "Relay", { rsa_fingerprint_hex: assertText(rsaFingerprintHex, "rsaFingerprintHex", 64) }, options);
  }

  /** The result x/relay computed for an epoch. */
  async relayEpochResult(epoch: Uint64Like, options?: QueryOptions): Promise<ChainQueryResult> {
    return this.moduleQuery(RELAY, "EpochResult", { epoch: assertUint64(epoch, "epoch") }, options);
  }

  // ---- a node's REST API ----

  /** Account number, sequence and public key: what a signature needs. */
  async account(address: string): Promise<ChainAccount> {
    const body = (await this.rest(`/cosmos/auth/v1beta1/accounts/${assertAddress(address)}`)) as {
      account?: { account_number?: string; sequence?: string; pub_key?: { key?: string } | null };
    };
    const acct = body.account;
    if (!acct) throw new SDKError("the chain returned no account", 502, "CHAIN_BAD_RESPONSE");
    const key = acct.pub_key?.key;
    return {
      accountNumber: BigInt(acct.account_number ?? "0"),
      sequence: BigInt(acct.sequence ?? "0"),
      publicKey: key ? base64ToBytes(key) : undefined,
    };
  }

  /** Bank balances of an address. Earnings are a separate account and are not here. */
  async balances(address: string): Promise<Array<{ denom: string; amount: string }>> {
    const body = (await this.rest(`/cosmos/bank/v1beta1/balances/${assertAddress(address)}`)) as {
      balances?: Array<{ denom: string; amount: string }>;
    };
    return body.balances ?? [];
  }

  /** Broadcasts a signed transaction and waits for CheckTx. A non-zero code throws. */
  async broadcast(txBytes: Uint8Array): Promise<BroadcastResult> {
    const body = (await this.rest("/cosmos/tx/v1beta1/txs", {
      method: "POST",
      body: JSON.stringify({ tx_bytes: bytesToBase64(txBytes), mode: "BROADCAST_MODE_SYNC" }),
    })) as { tx_response?: { code?: number; txhash?: string; raw_log?: string } };
    const res = body.tx_response;
    if (!res?.txhash) throw new SDKError("the chain returned no transaction response", 502, "CHAIN_BAD_RESPONSE");
    if (res.code) {
      throw new SDKError(`the chain rejected the transaction (code ${res.code}): ${printable(res.raw_log ?? "").slice(0, 200)}`, 400, "CHAIN_TX_REJECTED", {
        code: res.code,
        txHash: res.txhash,
      });
    }
    return { txHash: boundHash(res.txhash, txBytes), code: 0, rawLog: res.raw_log ?? "" };
  }

  /**
   * Reads the signer's account, builds the transaction, has the signer sign it
   * and broadcasts it. The signer sees the decoded transaction before it signs.
   */
  async signAndBroadcast(
    msgs: readonly AnyMsg[],
    signer: OramaSigner,
    options: SignAndBroadcastOptions,
  ): Promise<{ signed: SignedTx; result: BroadcastResult }> {
    const account = await this.account(signer.address);
    if (account.publicKey && !bytesEqual(account.publicKey, signer.publicKey)) {
      throw new Error(`the chain knows a different public key for ${signer.address} than the signer holds`);
    }
    const signed = await signTx(
      {
        chainId: options.chainId,
        accountNumber: account.accountNumber,
        sequence: account.sequence,
        gasLimit: options.gasLimit,
        feeNorama: options.feeNorama,
        msgs,
        memo: options.memo,
      },
      signer,
    );
    return { signed, result: await this.broadcast(signed.txBytes) };
  }

  /**
   * Sends ORAMA, privately unless `request.public` is `true`. A private transfer needs
   * `options.shielded`; without one it throws and never falls back to a public payment. See
   * {@link TransferRequest}.
   */
  transfer(request: TransferRequest, options?: TransferOptions): Promise<TransferResult> {
    return transfer(this, request, options);
  }

  /** Moves norama of the signer's earnings to the signer's own bank balance (MsgWithdrawEarnings). */
  withdrawEarnings(
    amount: bigint | number | string,
    signer: OramaSigner,
    options: SignAndBroadcastOptions,
  ): Promise<{ txHash: string; withdrawn: string }> {
    return withdrawEarnings(this, amount, signer, options);
  }

  // ---- transport ----

  private async gateway(path: string): Promise<unknown> {
    const base = requireBase(this.config.gatewayURL, "gatewayURL", "this read");
    return this.request(`${base}/v1/chain/${path}`);
  }

  private async post(route: string, txBytes: Uint8Array): Promise<unknown> {
    const base = requireBase(this.config.gatewayURL, "gatewayURL", "this call");
    return this.request(`${base}/v1/chain/${route}`, { method: "POST", body: JSON.stringify({ tx_bytes: bytesToBase64(txBytes) }) });
  }

  private async rest(path: string, init: RequestInit = {}): Promise<unknown> {
    const base = requireBase(this.config.restURL, "restURL", "this call");
    return this.request(`${base}${path}`, init);
  }

  private async request(url: string, init: RequestInit = {}): Promise<unknown> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.config.timeoutMs ?? DEFAULT_TIMEOUT_MS);
    let response: Response;
    try {
      response = await this.fetchFn(url, {
        ...init,
        headers: { Accept: "application/json", ...(init.body ? { "Content-Type": "application/json" } : {}) },
        signal: controller.signal,
      });
    } catch (err) {
      throw new NetworkError(`could not reach the chain at ${new URL(url).host}: ${(err as Error).message}`);
    } finally {
      clearTimeout(timer);
    }
    const text = await response.text();
    let body: unknown;
    try {
      body = text ? JSON.parse(text) : undefined;
    } catch {
      if (response.ok) throw new SDKError(`${new URL(url).host} did not answer JSON`, 502, "CHAIN_BAD_RESPONSE");
    }
    if (response.status === 422 && isTxRefusal(body)) throw new ChainTxRefusedError(body);
    if (!response.ok) throw SDKError.fromResponse(response.status, body);
    return body;
  }
}

/**
 * A 64-bit integer the gateway answers: a decimal string, or a number that is a safe integer. A bare
 * number above 2^53 has already lost its low digits in JSON.parse, so it is refused, not rounded.
 */
function gatewayUint64(value: unknown, field: string): bigint {
  if (typeof value === "string" && /^\d+$/.test(value)) return BigInt(value);
  if (typeof value === "number" && Number.isSafeInteger(value) && value >= 0) return BigInt(value);
  throw new SDKError(`the gateway answered ${field} as ${JSON.stringify(value)}, not a decimal string`, 502, "CHAIN_BAD_RESPONSE");
}

function isTxRefusal(body: unknown): body is { code: number; codespace?: string; log?: string; tx_hash?: string } {
  return typeof body === "object" && body !== null && typeof (body as { code?: unknown }).code === "number";
}

/**
 * The hash a broadcast answered must be the hash of the bytes that were sent. A node or gateway that
 * answers another would have the caller poll for, and report, some other transaction. Returns the
 * locally computed hash.
 */
function boundHash(answered: unknown, txBytes: Uint8Array): string {
  const local = txHashOf(txBytes);
  const given = typeof answered === "string" ? answered.replace(/^0x/i, "").toUpperCase() : "";
  if (given !== local) {
    const shown = String(answered).replace(/[^\x20-\x7e]/g, "").slice(0, 80);
    throw new SDKError(
      `the chain answered the transaction hash "${shown}" for a transaction whose hash is ${local}; refusing to follow or report it`,
      502,
      "CHAIN_TX_HASH_MISMATCH",
      { expected: local },
    );
  }
  return local;
}

function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

function bytesToBase64(bytes: Uint8Array): string {
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}
