import { showAttr, txHashFromBase64 } from "./format";

const BASE = "/v1/chain";

export class ChainReadError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ChainReadError";
  }
}

export interface ChainStatus {
  chainId: string;
  height: number;
  time: string;
  catchingUp: boolean;
}

export interface ChainBlockMeta {
  height: number;
  time: string;
  numTxs: number;
}

export interface ChainBlock {
  height: number;
  time: string;
  chainId: string;
  proposer: string;
  appHash: string;
  txHashes: string[];
}

export interface ChainEvent {
  type: string;
  attributes: { key: string; value: string }[];
}

export interface ChainTx {
  hash: string;
  height: number;
  index: number;
  code: number;
  gasWanted: number | null;
  gasUsed: number | null;
  log: string;
  codespace: string;
  events: ChainEvent[];
}

export interface ChainValidator {
  address: string;
  power: bigint;
  priority: bigint;
}

export interface ValidatorSet {
  height: number;
  totalPower: bigint;
  validators: ChainValidator[];
}

export interface Supply {
  amount: string;
}

export interface Pool {
  bonded: string;
  notBonded: string;
}

function clip(text: string): string {
  const trimmed = text.trim();
  return trimmed.length > 2000 ? `${trimmed.slice(0, 2000)}…` : trimmed;
}

function errorText(text: string, status: number): string {
  const trimmed = text.trim();
  if (!trimmed) return String(status);
  try {
    const body = JSON.parse(trimmed) as { error?: unknown; message?: unknown };
    if (body.error != null) return clip(rpcErrorMessage(body.error));
    if (typeof body.message === "string" && body.message) return clip(body.message);
  } catch {
    // The proxy's own refusals are plain text.
  }
  return clip(trimmed);
}

async function getJSON(path: string): Promise<unknown> {
  let res: Response;
  try {
    res = await fetch(`${BASE}${path}`, {
      method: "GET",
      headers: { Accept: "application/json" },
      cache: "no-store",
      credentials: "omit",
    });
  } catch (err) {
    throw new ChainReadError(err instanceof Error && err.message ? err.message : "network error");
  }
  const text = await res.text();
  if (!res.ok) throw new ChainReadError(errorText(text, res.status));
  try {
    return JSON.parse(text) as unknown;
  } catch {
    throw new ChainReadError("chain response was not JSON");
  }
}

function rec(v: unknown, name: string): Record<string, unknown> {
  if (v === null || typeof v !== "object" || Array.isArray(v)) {
    throw new ChainReadError(`${name} is not an object`);
  }
  return v as Record<string, unknown>;
}

function rpcErrorMessage(error: unknown): string {
  if (error === null || typeof error !== "object") return "chain RPC error";
  const e = error as { message?: unknown; data?: unknown };
  const parts = [e.message, e.data].filter((p): p is string => typeof p === "string" && p.length > 0);
  return parts.join(": ") || "chain RPC error";
}

function rpcResult(body: unknown): unknown {
  const root = rec(body, "chain response");
  if ("error" in root && root.error != null) throw new ChainReadError(rpcErrorMessage(root.error));
  if (!("result" in root) || root.result == null) throw new ChainReadError("chain RPC response has no result");
  return root.result;
}

function integerString(v: unknown, name: string, neg: boolean): string {
  let s: string;
  if (typeof v === "number") {
    if (!Number.isSafeInteger(v)) throw new ChainReadError(`${name} is too large`);
    s = String(v);
  } else if (typeof v === "string") {
    s = v;
  } else {
    throw new ChainReadError(`${name} is not an integer`);
  }
  const re = neg ? /^-?(0|[1-9][0-9]*)$/ : /^(0|[1-9][0-9]*)$/;
  if (!re.test(s)) throw new ChainReadError(`${name} is not an integer`);
  return s;
}

function safeInt(v: unknown, name: string): number {
  const s = integerString(v, name, false);
  const n = Number(s);
  if (!Number.isSafeInteger(n)) throw new ChainReadError(`${name} is too large`);
  return n;
}

function intBig(v: unknown, name: string, neg = false): bigint {
  return BigInt(integerString(v, name, neg));
}

function text(v: unknown, name: string): string {
  if (typeof v !== "string" || v.length === 0) throw new ChainReadError(`${name} is missing`);
  if (/[\u0000-\u001f\u007f]/.test(v)) throw new ChainReadError(`${name} is not text`);
  return v;
}

function optionalText(v: unknown): string {
  if (v == null) return "";
  if (typeof v !== "string") throw new ChainReadError("text field is not a string");
  // Newlines show up in a transaction log. Other controls do not.
  if (/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/.test(v)) throw new ChainReadError("text field is not text");
  return v;
}

function hexOrEmpty(v: unknown, name: string): string {
  if (v == null || v === "") return "";
  if (typeof v !== "string" || !/^[0-9a-fA-F]{2,128}$/.test(v) || v.length % 2 !== 0) {
    throw new ChainReadError(`${name} is not hex`);
  }
  return v.toUpperCase();
}

function addressHex(v: unknown, name: string): string {
  const s = text(v, name);
  if (!/^[0-9a-fA-F]{40}$/.test(s)) throw new ChainReadError(`${name} is not a consensus address`);
  return s.toUpperCase();
}

function chainIdOf(v: unknown): string {
  const s = text(v, "chain id");
  if (!/^[A-Za-z0-9._-]{1,64}$/.test(s)) throw new ChainReadError("chain id is not usable");
  return s;
}

function query(params: Record<string, string>): string {
  const s = new URLSearchParams(params).toString();
  return s ? `?${s}` : "";
}

export async function getStatus(): Promise<ChainStatus> {
  const root = rec(rpcResult(await getJSON("/status")), "status");
  const node = rec(root.node_info, "node_info");
  const sync = rec(root.sync_info, "sync_info");
  if (typeof sync.catching_up !== "boolean") throw new ChainReadError("catching_up is missing");
  return {
    chainId: chainIdOf(node.network),
    height: safeInt(sync.latest_block_height, "latest_block_height"),
    time: text(sync.latest_block_time, "latest_block_time"),
    catchingUp: sync.catching_up,
  };
}

export async function getBlockMetas(min: number, max: number): Promise<ChainBlockMeta[]> {
  const root = rec(rpcResult(await getJSON(`/blocks${query({ min_height: String(min), max_height: String(max) })}`)), "blockchain");
  const raw = root.block_metas;
  if (raw == null) return [];
  if (!Array.isArray(raw)) throw new ChainReadError("block_metas is not a list");
  const metas = raw.map((item, i) => {
    const meta = rec(item, `block meta ${i}`);
    const header = rec(meta.header, `block meta ${i} header`);
    const height = safeInt(header.height, "height");
    if (height < min || height > max) throw new ChainReadError("block meta is outside the requested range");
    return {
      height,
      time: text(header.time, "time"),
      numTxs: safeInt(meta.num_txs, "num_txs"),
    };
  });
  metas.sort((a, b) => a.height - b.height);
  return metas;
}

export async function getBlock(height: number): Promise<ChainBlock> {
  const root = rec(rpcResult(await getJSON(`/block${query({ height: String(height) })}`)), "block");
  const block = rec(root.block, "block");
  const header = rec(block.header, "header");
  const data = rec(block.data, "data");
  const got = safeInt(header.height, "height");
  if (got !== height) throw new ChainReadError("block height did not match the request");
  const rawTxs = data.txs;
  const txs: string[] = [];
  if (rawTxs != null) {
    if (!Array.isArray(rawTxs)) throw new ChainReadError("block txs is not a list");
    for (const [i, tx] of rawTxs.entries()) {
      if (typeof tx !== "string") throw new ChainReadError(`tx ${i} is not base64`);
      txs.push(tx);
    }
  }
  const txHashes = await Promise.all(txs.map((tx) => txHashFromBase64(tx)));
  return {
    height: got,
    time: text(header.time, "time"),
    chainId: chainIdOf(header.chain_id),
    proposer: hexOrEmpty(header.proposer_address, "proposer_address"),
    appHash: hexOrEmpty(header.app_hash, "app_hash"),
    txHashes,
  };
}

function eventsOf(v: unknown): ChainEvent[] {
  if (v == null) return [];
  if (!Array.isArray(v)) throw new ChainReadError("events is not a list");
  return v.map((item, i) => {
    const ev = rec(item, `event ${i}`);
    const rawAttrs = ev.attributes;
    const attributes: { key: string; value: string }[] = [];
    if (rawAttrs != null) {
      if (!Array.isArray(rawAttrs)) throw new ChainReadError(`event ${i} attributes is not a list`);
      for (const [j, attr] of rawAttrs.entries()) {
        const a = rec(attr, `event ${i} attribute ${j}`);
        attributes.push({
          key: showAttr(optionalText(a.key)),
          value: showAttr(optionalText(a.value)),
        });
      }
    }
    return { type: showAttr(optionalText(ev.type)), attributes };
  });
}

export async function getTx(hash: string): Promise<ChainTx> {
  const want = hash.replace(/^0x/i, "").toUpperCase();
  const root = rec(rpcResult(await getJSON(`/tx${query({ hash: want })}`)), "tx");
  const result = rec(root.tx_result, "tx_result");
  const got = text(root.hash, "hash").toUpperCase();
  if (got !== want) throw new ChainReadError("transaction hash did not match the request");
  return {
    hash: got,
    height: safeInt(root.height, "height"),
    index: root.index == null ? 0 : safeInt(root.index, "index"),
    code: result.code == null ? 0 : safeInt(result.code, "code"),
    gasWanted: result.gas_wanted == null ? null : safeInt(result.gas_wanted, "gas_wanted"),
    gasUsed: result.gas_used == null ? null : safeInt(result.gas_used, "gas_used"),
    log: optionalText(result.log),
    codespace: optionalText(result.codespace),
    events: eventsOf(result.events),
  };
}

interface ValidatorPage {
  height: number;
  total: number;
  validators: ChainValidator[];
}

async function getValidatorPage(page: number): Promise<ValidatorPage> {
  const root = rec(
    rpcResult(await getJSON(`/validators${query({ page: String(page), per_page: "100" })}`)),
    "validators",
  );
  const raw = root.validators;
  const validators: ChainValidator[] = [];
  if (raw != null) {
    if (!Array.isArray(raw)) throw new ChainReadError("validators is not a list");
    for (const [i, item] of raw.entries()) {
      const v = rec(item, `validator ${i}`);
      validators.push({
        address: addressHex(v.address, "address"),
        power: intBig(v.voting_power, "voting_power"),
        priority: v.proposer_priority == null ? 0n : intBig(v.proposer_priority, "proposer_priority", true),
      });
    }
  }
  return {
    height: safeInt(root.block_height, "block_height"),
    total: safeInt(root.total, "total"),
    validators,
  };
}

export async function getValidators(): Promise<ValidatorSet> {
  const pages: ChainValidator[] = [];
  let height = 0;
  let total = 0;
  for (let page = 1; page <= 10; page++) {
    const got = await getValidatorPage(page);
    if (page === 1) {
      height = got.height;
      total = got.total;
    }
    pages.push(...got.validators);
    if (pages.length >= total) break;
    if (got.validators.length === 0) {
      throw new ChainReadError("validator page ended before the set was complete");
    }
  }
  if (pages.length !== total) {
    throw new ChainReadError(
      pages.length < total ? "validator set is larger than this page walk" : "validator pages returned more than the set size",
    );
  }
  const totalPower = pages.reduce((sum, v) => sum + v.power, 0n);
  return { height, totalPower, validators: pages };
}

export async function getSupply(): Promise<Supply> {
  const root = rec(await getJSON("/supply/norama"), "supply");
  const amount = rec(root.amount, "amount");
  if (amount.denom !== "norama") throw new ChainReadError("supply denom is not norama");
  return { amount: integerString(amount.amount, "amount", false) };
}

export async function getPool(): Promise<Pool> {
  const root = rec(await getJSON("/staking/pool"), "pool response");
  const pool = rec(root.pool, "pool");
  const bonded = pool.bonded_tokens ?? pool.bondedTokens;
  const notBonded = pool.not_bonded_tokens ?? pool.notBondedTokens;
  return {
    bonded: integerString(bonded, "bonded_tokens", false),
    notBonded: integerString(notBonded, "not_bonded_tokens", false),
  };
}
