import { bech32Encode } from "../../model/bech32";
import { consensusAddress } from "./directory";

/** A stand-in for the gateway's /v1/chain/ proxy, for tests: it answers by path and counts what was asked. */

export const addr = (n: number) => bech32Encode("orama", new Uint8Array(20).fill(n));
export const valoper = (n: number) => bech32Encode("oramavaloper", new Uint8Array(20).fill(n));

export const HEAD = 100;
export const HEAD_TIME = "2026-10-08T12:00:00Z";
export const CHAIN_ID = "orama-test-1";

/** The base64 of a 32-byte ed25519 key made of one repeated byte. */
export const keyOf = (n: number) => btoa(String.fromCharCode(...new Uint8Array(32).fill(n)));

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
    const path = input.replace(/^\/v1\/chain\//, "");
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
      { operator_address: valoper(7), consensus_pubkey: { "@type": "/cosmos.crypto.ed25519.PubKey", key: keyOf(11) }, jailed: false, description: { moniker: "Alpha" } },
      { operator_address: valoper(8), consensus_pubkey: { "@type": "/cosmos.crypto.ed25519.PubKey", key: keyOf(12) }, jailed: true, description: { moniker: "Beta‮" } },
    ],
  });
  gw.on("query/orama.power.v1.Query/BootstrapCommittee", { members: [{ operator_address: addr(7), moniker: "Alpha" }] });
  gw.on("query/orama.power.v1.Query/Lambda", { lambda: "0.250000000000000000" });
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
  return gw;
}
