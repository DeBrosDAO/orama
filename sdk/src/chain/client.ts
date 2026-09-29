import { NetworkError, SDKError } from "../errors";
import type { AnyMsg } from "./msg";
import type { OramaSigner } from "./signer";
import { signTx, type SignedTx } from "./tx";

/** Where the chain is read from. Each read names the base it uses. */
export interface ChainClientConfig {
  /** The gateway root, for the read-only /v1/chain/ proxy: status, blocks, transactions, validators, the indexer. */
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

const DEFAULT_TIMEOUT_MS = 15_000;
const HEX_HASH = /^(0x)?[0-9a-fA-F]{64}$/;
const ORAMA_ADDRESS = /^orama1[02-9ac-hj-np-z]{6,90}$/;

function requireBase(base: string | undefined, name: string, use: string): string {
  if (!base) throw new Error(`${use} needs ${name} in the chain client config`);
  return base.replace(/\/+$/, "");
}

function assertAddress(address: string): string {
  if (!ORAMA_ADDRESS.test(address)) throw new Error(`"${address}" is not an orama address`);
  return address;
}

function assertHash(hash: string): string {
  if (!HEX_HASH.test(hash)) throw new Error("a transaction hash is 64 hex characters");
  return hash.replace(/^0x/, "").toLowerCase();
}

function assertUint(value: number, name: string): number {
  if (!Number.isSafeInteger(value) || value < 0) throw new Error(`${name} must be a non-negative integer`);
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
 * modules' own state (x/nodes, x/storage, x/fees) speaks gRPC only and has no
 * REST route, so it is not read here; the CLI's `orama chain query` reads it
 * through a node's CometBFT RPC.
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
      throw new SDKError(`the chain rejected the transaction (code ${res.code}): ${(res.raw_log ?? "").slice(0, 200)}`, 400, "CHAIN_TX_REJECTED", {
        code: res.code,
        txHash: res.txhash,
      });
    }
    return { txHash: res.txhash, code: 0, rawLog: res.raw_log ?? "" };
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

  // ---- transport ----

  private async gateway(path: string): Promise<unknown> {
    const base = requireBase(this.config.gatewayURL, "gatewayURL", "this read");
    return this.request(`${base}/v1/chain/${path}`);
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
    if (!response.ok) throw SDKError.fromResponse(response.status, body);
    return body;
  }
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
