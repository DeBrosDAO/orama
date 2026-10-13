import { addr, chainFixture, CHAIN_ID, HEAD, HEAD_TIME, rpc } from "../data/chain/fake-gateway";
import type { FakeGateway } from "../data/chain/fake-gateway";
import { NODE_PARAMS, RELAY_PARAMS, STORAGE_PARAMS, ARCHIVE_PARAMS, page, relayJson, retainJson } from "../data/chain/fake-network";
import {
  CLOSED_EPOCHS,
  DEV_BLOCKS,
  accountCredit,
  blockRow,
  devAccount,
  devDeals,
  devHome,
  devNodes,
  devRelays,
  epochRow,
  reward,
  supplyPoint,
  txByNumber,
  validatorEpoch,
  validatorSummary,
} from "./dev-data";

/** What the dev gateway answers: a status and a JSON body, as the gateway's /v1/chain/ proxy would. */
export interface DevAnswer {
  status: number;
  body: unknown;
}

const NOT_FOUND: DevAnswer = { status: 404, body: { error: { code: "NOT_FOUND", message: "not found on chain" } } };

type Params = URLSearchParams;
type Handler = (match: RegExpExecArray, params: Params) => unknown;

/** The `json` parameter of a module query, as the explorer writes it. */
function jsonParam(params: Params): Record<string, unknown> {
  const raw = params.get("json");
  return raw === null ? {} : (JSON.parse(raw) as Record<string, unknown>);
}

/** A CometBFT block at any height: the proposer is the first validator, and two of three validators signed. */
function blockHeader(height: number) {
  return {
    header: { height: String(height), time: blockRow(height).time, proposer_address: "ab".repeat(20) },
    data: { txs: [] },
    last_commit: { signatures: [{ block_id_flag: 2 }, { block_id_flag: 2 }, { block_id_flag: 1 }] },
  };
}

function indexRoutes(): [RegExp, Handler][] {
  const closed = Array.from({ length: CLOSED_EPOCHS }, (_, i) => CLOSED_EPOCHS - i);
  const limitOf = (p: Params, fallback: number) => Number(p.get("limit") ?? fallback);
  return [
    [/^block$/, (_m, p) => rpc({ block_id: { hash: "AB".repeat(32) }, block: blockHeader(Number(p.get("height"))) })],
    [/^index\/status$/, () => devHome().status],
    [/^index\/home$/, () => devHome()],
    [/^index\/blocks$/, (_m, p) => ({ blocks: Array.from({ length: Math.min(limitOf(p, 100), DEV_BLOCKS) }, (_, i) => blockRow(DEV_BLOCKS - i)) })],
    [/^index\/blocks\/(\d+)$/, (m) => ({ ...blockRow(Number(m[1])), tx_hashes: [], gas_used: 80000 })],
    [/^index\/txs$/, (_m, p) => ({ txs: [txByNumber(1), txByNumber(2), txByNumber(3)].slice(0, limitOf(p, 10)) })],
    [/^index\/txs\/([0-9a-f]{64})$/, (m) => txByNumber(Number.parseInt(m[1], 16))],
    [/^index\/accounts\/([^/]+)$/, () => ({ address: devAccount, tx_count: 7, first_seen: "2026-10-01T00:00:00Z", last_active: "2026-10-08T11:00:00Z" })],
    [/^index\/accounts\/([^/]+)\/txs$/, () => ({ txs: [txByNumber(1), txByNumber(2), txByNumber(3)] })],
    [/^index\/accounts\/([^/]+)\/rewards$/, () => ({ recorded_from_epoch: 1, rewards: closed.slice(0, 5).map(accountCredit) })],
    [/^index\/epochs$/, (_m, p) => ({ page: 1, limit: limitOf(p, 20), epochs: closed.slice(0, limitOf(p, 20)).map((e) => epochRow(e)) })],
    [/^index\/epochs\/(\d+)$/, (m) => (Number(m[1]) <= CLOSED_EPOCHS && Number(m[1]) > 0 ? epochRow(Number(m[1])) : null)],
    [/^index\/epochs\/(\d+)\/validators$/, (m) => ({ epoch: Number(m[1]), validators: [validatorEpoch(Number(m[1]), 7), validatorEpoch(Number(m[1]), 8)] })],
    [/^index\/epochs\/(\d+)\/rewards$/, (m) => ({ epoch: Number(m[1]), recorded: true, page: 1, limit: 25, rewards: [reward(Number(m[1]), 7), reward(Number(m[1]), 8)] })],
    [/^index\/supply$/, () => ({ page: 1, limit: 100, points: closed.map(supplyPoint) })],
    [/^index\/validators$/, () => ({ page: 1, limit: 100, validators: [validatorSummary(7), validatorSummary(8)] })],
    [/^index\/validators\/([^/]+)\/epochs$/, () => ({ epochs: closed.map((e) => validatorEpoch(e, 7)) })],
    [/^index\/validators\/([^/]+)\/slashes$/, () => ({ slashes: [] })],
    [/^index\/validators\/([^/]+)\/jails$/, () => ({ jails: [] })],
    [/^index\/validators\/([^/]+)\/rewards$/, () => ({ rewards: closed.slice(0, 5).map((e) => ({ epoch: e, recorded: true, reward: reward(e, 7) })) })],
  ];
}

function queryRoutes(): [RegExp, Handler][] {
  const coin = { denom: "norama", amount: "12500000000" };
  return [
    [/^query\/cosmos\.bank\.v1beta1\.Query\/AllBalances$/, () => ({ balances: [coin] })],
    [/^query\/cosmos\.staking\.v1beta1\.Query\/DelegatorDelegations$/, () => ({ delegation_responses: [{ balance: { denom: "norama", amount: "1500000000000" } }] })],
    [/^query\/cosmos\.staking\.v1beta1\.Query\/DelegatorUnbondingDelegations$/, () => ({ unbonding_responses: [] })],
    [/^query\/cosmos\.staking\.v1beta1\.Query\/Delegation$/, () => ({ delegation_response: { balance: { denom: "norama", amount: "1500000000000" } } })],
    [/^query\/orama\.power\.v1\.Query\/ValidatorPower$/, (_m, p) => ({ operator_address: String(jsonParam(p).operator_address), comet_power: "600000000", operator: devAccount })],
    [/^query\/orama\.fees\.v1\.Query\/Earnings$/, () => ({ balance: "12345" })],
    [/^query\/orama\.nodes\.v1\.Query\/Params$/, () => NODE_PARAMS],
    [/^query\/orama\.nodes\.v1\.Query\/Nodes$/, () => page("nodes", devNodes())],
    [/^query\/orama\.nodes\.v1\.Query\/Node$/, (_m, p) => ({ node: devNodes().find((n) => n.node_id === jsonParam(p).node_id) ?? null })],
    [/^query\/orama\.nodes\.v1\.Query\/NameOfNode$/, () => ({ name: { name: "alpha", node_id: "node-1", operator: addr(1), deposit: "1000000000" } })],
    [/^query\/orama\.nodes\.v1\.Query\/NodeUnbondings$/, () => ({ unbondings: [] })],
    [/^query\/orama\.storage\.v1\.Query\/Params$/, () => STORAGE_PARAMS],
    [/^query\/orama\.storage\.v1\.Query\/Deals$/, () => page("deals", devDeals())],
    [/^query\/orama\.storage\.v1\.Query\/Queue$/, () => ({ pending: "12", head: "3", tail: "15" })],
    [/^query\/orama\.archive\.v1\.Query\/Params$/, () => ARCHIVE_PARAMS],
    [/^query\/orama\.archive\.v1\.Query\/RetainHeight$/, () => retainJson()],
    [/^query\/orama\.relay\.v1\.Query\/Params$/, () => RELAY_PARAMS],
    [/^query\/orama\.relay\.v1\.Query\/Relays$/, () => page("relays", devRelays())],
    [/^query\/orama\.relay\.v1\.Query\/Reporters$/, () => ({ reporters: [addr(30), addr(31)] })],
    [
      /^query\/orama\.relay\.v1\.Query\/RelayPayouts$/,
      (_m, p) => page("payouts", [{ epoch: String(jsonParam(p).epoch), rsa_fingerprint: relayJson(1).rsa_fingerprint, operator: addr(1), amount: "250000000" }]),
    ],
    [/^query\/orama\.relay\.v1\.Query\/EpochResult$/, (_m, p) => ({ result: { epoch: String(jsonParam(p).epoch), quorum_met: true, activating: false, ceiling: "1000", minted: "400" } })],
    [/^query\/cosmos\.bank\.v1beta1\.Query\/TotalSupply$/, () => ({ supply: [{ denom: "norama", amount: "41000000000000000" }], pagination: { next_key: null } })],
    [/^query\/orama\.shielded\.v1\.Query\/Params$/, () => ({ params: { enabled: true } })],
  ];
}

/** The routes by pattern, tried after the chain fixture's own exact routes. */
const PATTERNS: [RegExp, Handler][] = [...indexRoutes(), ...queryRoutes()];

let fixture: Promise<FakeGateway> | null = null;

/** The head the gateway reports; the stream announces it. */
export const devHead = () => ({ height: HEAD, time: HEAD_TIME, chain_id: CHAIN_ID });

/**
 * One GET of the /v1/chain/ proxy, answered from a fixed chain. `path` is what follows /v1/chain/,
 * query string included. An unanswered path is a 404, which is how a page shows its not-found state.
 */
export async function devAnswer(path: string): Promise<DevAnswer> {
  fixture ??= chainFixture().then((gw) =>
    gw.on("query/orama.emission.v1.Query/Params", { params: { epoch_duration_seconds: "86400", min_blocks_per_epoch: "14400" } }),
  );
  const gw = await fixture;
  const exact = gw.routes.get(path);
  if (exact !== undefined) return { status: 200, body: typeof exact === "function" ? exact(path) : exact };
  const [bare, query = ""] = path.split("?");
  const params = new URLSearchParams(query);
  for (const [pattern, handler] of PATTERNS) {
    const match = pattern.exec(bare);
    if (match === null) continue;
    const body = handler(match, params);
    return body === null ? NOT_FOUND : { status: 200, body };
  }
  if (bare === "validators") return { status: 200, body: rpc({ validators: [], total: "0" }) };
  return NOT_FOUND;
}
