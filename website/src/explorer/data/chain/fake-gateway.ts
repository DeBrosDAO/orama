import { bech32Encode } from "../../model/bech32";
import { withQuery } from "./client";
import { consensusAddress } from "./directory";
import type { EventSourceFactory, EventSourceLike } from "./head-feed";

/** A stand-in for the gateway's /v1/chain/ proxy, for tests: it answers by path and counts what was asked. */

export const addr = (n: number) => bech32Encode("orama", new Uint8Array(20).fill(n));
export const valoper = (n: number) => bech32Encode("oramavaloper", new Uint8Array(20).fill(n));

export const HEAD = 100;
export const HEAD_TIME = "2026-10-08T12:00:00Z";
export const CHAIN_ID = "orama-test-1";

/** The base64 of a 32-byte ed25519 key made of one repeated byte. */
export const keyOf = (n: number) => btoa(String.fromCharCode(...new Uint8Array(32).fill(n)));

/** The paths of the module queries a validator page makes, as the client builds them. */
export const powerPath = (valoperAddress: string) =>
  withQuery("query/orama.power.v1.Query/ValidatorPower", { json: JSON.stringify({ operator_address: valoperAddress }) });
export const delegationPath = (delegator: string, valoperAddress: string) =>
  withQuery("query/cosmos.staking.v1beta1.Query/Delegation", { json: JSON.stringify({ delegator_addr: delegator, validator_addr: valoperAddress }) });

/** A validator as the node's REST staking list writes it (decimals with a point). */
export function stakingValidator(n: number, keyByte: number, over: Record<string, unknown> = {}) {
  return {
    operator_address: valoper(n),
    consensus_pubkey: { "@type": "/cosmos.crypto.ed25519.PubKey", key: keyOf(keyByte) },
    jailed: false,
    status: "BOND_STATUS_BONDED",
    tokens: "2000000000000",
    delegator_shares: "2000000000000.000000000000000000",
    description: { moniker: `V${n}`, identity: "", website: "", security_contact: "", details: "" },
    unbonding_height: "0",
    unbonding_time: "1970-01-01T00:00:00Z",
    commission: { commission_rates: { rate: "0.100000000000000000", max_rate: "0.200000000000000000", max_change_rate: "0.010000000000000000" }, update_time: "1970-01-01T00:00:00Z" },
    min_self_delegation: "1",
    ...over,
  };
}

/** x/power's Params as the module-query route writes them: decimals unscaled, as on the live gateway. */
export const POWER_PARAMS = {
  params: {
    min_committee_size: "5",
    bootstrap_exit_stake: "271000000000000",
    bootstrap_deadline_epochs: "365",
    cap_fraction_normal: "50000000000000000",
    cap_fraction_reduced: "30000000000000000",
    cap_step_down_validator_count: "60",
    cap_step_up_validator_count: "50",
    cap_hysteresis_epochs: "30",
    pre_gate_lambda_cap: "950000000000000000",
  },
};

/** Two indexer cursors (chain/indexer/cursor.go): 16 characters of the URL-safe alphabet naming a position in a list. */
export const CURSOR_A = "AAAAAAAAAAAAA-Dk";
export const CURSOR_B = "AAAAAAAAAAAAA_Fw";

export const hashOf = (n: number) => n.toString(16).padStart(64, "0");

export const rpc = (result: unknown) => ({ jsonrpc: "2.0", id: -1, result });

export function indexTx(n: number, over: Record<string, unknown> = {}) {
  return {
    hash: hashOf(n),
    height: HEAD - 1,
    index: n,
    code: 0,
    gas_wanted: 200000,
    gas_used: 80000,
    time: "2026-10-08T11:59:50Z",
    signer: addr(1),
    memo: "",
    messages: ["/cosmos.bank.v1beta1.MsgSend"],
    body: [{ "@type": "/cosmos.bank.v1beta1.MsgSend", from_address: addr(1), to_address: addr(2), amount: [{ denom: "norama", amount: "5000000000" }] }],
    events: [{ type: "tx", attributes: [{ key: "base_fee", value: "100" }, { key: "tip", value: "0" }] }],
    ...over,
  };
}

export type Handler = (path: string) => unknown;

export class FakeGateway {
  readonly asked: string[] = [];
  readonly routes = new Map<string, Handler | unknown>();
  failing = new Set<string>();

  on(path: string, answer: Handler | unknown): this {
    this.routes.set(path, answer);
    return this;
  }

  readonly fetch = async (input: string): Promise<Response> => {
    const path = input.replace(/^(https?:\/\/[^/]+)?\/v1\/chain\//, "");
    this.asked.push(path);
    if (this.failing.has(path)) return new Response("open /var/lib/oramad: corrupted", { status: 500 });
    const route = this.routes.get(path);
    if (route === undefined) return new Response("not found on chain", { status: 404 });
    const body = typeof route === "function" ? (route as Handler)(path) : route;
    return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
  };
}

/** A gateway with a two-validator chain at HEAD and an indexer, enough for every page's reads. */
export async function chainFixture(): Promise<FakeGateway> {
  const gw = new FakeGateway();
  const [c1, c2] = [await consensusAddress(keyOf(11)), await consensusAddress(keyOf(12))];
  gw.on("status", rpc({
    node_info: { network: CHAIN_ID },
    sync_info: { latest_block_height: String(HEAD), latest_block_time: HEAD_TIME, catching_up: false },
  }));
  gw.on("validators?page=1&per_page=100", rpc({
    validators: [{ address: c1, voting_power: "60" }, { address: c2, voting_power: "40" }],
    total: "2",
  }));
  gw.on("staking/validators", {
    validators: [
      stakingValidator(7, 11, { description: { moniker: "Alpha" }, tokens: "4000000000000" }),
      stakingValidator(8, 12, { jailed: true, description: { moniker: "Beta‮" }, tokens: "3000000000000" }),
    ],
  });
  gw.on("query/orama.power.v1.Query/BootstrapCommittee", { members: [{ operator_address: addr(7), moniker: "Alpha" }] });
  gw.on("query/orama.power.v1.Query/Lambda", { lambda: "250000000000000000", last_updated_epoch: "12", current_cap_bps: "500", below_step_up_streak_epochs: "0" });
  gw.on("query/orama.power.v1.Query/Params", POWER_PARAMS);
  gw.on("staking/pool", { pool: { not_bonded_tokens: "0", bonded_tokens: "7000000000000" } });
  gw.on("supply/norama", { amount: { denom: "norama", amount: "41000000000000000" } });
  gw.on("query/orama.fees.v1.Query/BaseFee", { base_fee: "1000" });
  gw.on("query/orama.emission.v1.Query/CurrentEpoch", { epoch_state: { current_epoch: "12", epoch_start_unix_nano: String(Date.parse("2026-10-08T06:00:00Z") * 1_000_000) } });
  gw.on("query/orama.emission.v1.Query/Params", { params: { epoch_duration_seconds: "86400" } });
  const signatures = [{ block_id_flag: 2 }, { block_id_flag: 2 }, { block_id_flag: 1 }];
  gw.on(`block?height=${HEAD}`, rpc({
    block_id: { hash: "AB".repeat(32) },
    block: { header: { height: String(HEAD), time: HEAD_TIME, proposer_address: c1 }, data: { txs: [] }, last_commit: { signatures } },
  }));
  gw.on(`blocks?min_height=${HEAD - 19}&max_height=${HEAD}`, rpc({
    last_height: String(HEAD),
    block_metas: Array.from({ length: 20 }, (_, i) => ({
      block_id: { hash: "CD".repeat(32) },
      header: { height: String(HEAD - i), time: new Date(Date.parse(HEAD_TIME) - i * 2000).toISOString(), proposer_address: c1 },
      num_txs: String(i % 3),
    })),
  }));
  const hours = Array.from({ length: 48 }, (_, i) => ({ hour: new Date(Date.UTC(2026, 9, 6, 12 + i)).toISOString(), txs: i < 24 ? 10 : 15, failed: i < 24 ? 0 : 1, burned: i < 24 ? "0" : "200" }));
  gw.on("index/stats", { start_height: 1, hours });
  gw.on("index/home", homeJson({ hours }));
  return gw;
}

/** The indexer's home snapshot at HEAD (chain/indexer/home.go): 24 blocks two seconds apart, three transactions, two of three validators signing. */
export function homeJson(over: Record<string, unknown> = {}) {
  return {
    status: { start_height: 1, cursor: HEAD, earliest: 1, tip: HEAD, lag: 0, aggregates_from: 1 },
    blocks: Array.from({ length: 24 }, (_, i) => ({
      height: HEAD - i,
      hash: "cd".repeat(32),
      time: new Date(Date.parse(HEAD_TIME) - i * 2000).toISOString(),
      proposer: "ab".repeat(20),
      tx_count: i % 3,
      gas_used: 0,
      burned: "0",
      commit_signed: 2,
      commit_size: 3,
    })),
    txs: [indexTx(1), indexTx(2), indexTx(3)],
    hours: [],
    epoch: { number: 12, start_time: "2026-10-08T06:00:00Z", duration_seconds: 86400, ends_at: "2026-10-09T06:00:00Z" },
    supply: "41000000000000000",
    base_fee: "1000",
    supply_height: HEAD,
    examples: { latest_tx: hashOf(1), busy_account: addr(1) },
    ...over,
  };
}

/**
 * A stand-in for the browser's EventSource on the gateway's head stream. A stream opens, as the
 * gateway's does, with the current head; `push` sends another and `drop` is a lost connection.
 */
export class FakeStream {
  readonly opened: string[] = [];
  closed = 0;
  /** A gateway that is refusing streams sends no head: the stream is closed with its status. */
  withholdHead = false;
  private sources: FakeEventSource[] = [];

  constructor(private current: { height: number; time: string; chainId: string } = { height: HEAD, time: HEAD_TIME, chainId: CHAIN_ID }) {}

  readonly factory: EventSourceFactory = (url) => {
    this.opened.push(url);
    const source = new FakeEventSource(() => {
      this.closed++;
    });
    this.sources.push(source);
    queueMicrotask(() => {
      if (!this.withholdHead) source.emit("head", this.headData(this.current));
    });
    return source;
  };

  private headData(h: { height: number; time: string; chainId: string }): string {
    return JSON.stringify({ height: h.height, time: h.time, chain_id: h.chainId });
  }

  push(height: number, chainId: string = this.current.chainId): void {
    this.current = { height, time: HEAD_TIME, chainId };
    for (const s of this.sources) s.emit("head", this.headData(this.current));
  }

  /** Sends an event as the gateway wrote it, whatever it holds. */
  emitRaw(type: string, data: string): void {
    for (const s of this.sources) s.emit(type, data);
  }

  unavailable(): void {
    for (const s of this.sources) s.emit("unavailable", JSON.stringify({ message: "the chain could not be read" }));
  }

  drop(): void {
    for (const s of this.sources) if (!s.shut) s.onerror?.(new Event("error"));
  }

  /** The gateway refuses the stream (429 or 503): the browser closes the connection and does not retry. */
  refuse(): void {
    for (const s of this.sources) {
      if (s.shut) continue;
      s.readyState = 2;
      s.onerror?.(new Event("error"));
    }
  }

  get open(): number {
    return this.opened.length - this.closed;
  }
}

class FakeEventSource implements EventSourceLike {
  onerror: ((event: Event) => unknown) | null = null;
  readyState = 1;
  /** Closed by the page: a closed EventSource fires nothing. */
  shut = false;
  private listeners = new Map<string, ((event: { data?: unknown }) => void)[]>();

  constructor(private readonly onClose: () => void) {}

  addEventListener(type: string, listener: (event: { data?: unknown }) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }

  close(): void {
    this.shut = true;
    this.listeners.clear();
    this.onClose();
  }

  emit(type: string, data: string): void {
    for (const l of this.listeners.get(type) ?? []) l({ data });
  }
}
