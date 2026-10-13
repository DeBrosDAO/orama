import type { RelayEpoch, RelayParams, TorRelay, TorRelays } from "../../model/tor";
import type { Norama } from "../../model/types";
import { ChainNotFound, ChainReadError, withQuery } from "./client";
import type { ChainClient } from "./client";
import { listAll } from "./paging";
import { NODE_ID } from "./nodes";
import { clean } from "./text";
import { account, bool, digits, legacyDec, optArr, rec, str, uint } from "./wire";

const RELAYS = "query/orama.relay.v1.Query/Relays";
const RELAY_PARAMS = "query/orama.relay.v1.Query/Params";
const REPORTERS = "query/orama.relay.v1.Query/Reporters";
const EPOCH_RESULT = "query/orama.relay.v1.Query/EpochResult";
const RELAY_PAYOUTS = "query/orama.relay.v1.Query/RelayPayouts";
const CURRENT_EPOCH = "query/orama.emission.v1.Query/CurrentEpoch";

const RELAY_PAGES = 5;
/** x/relay settles epoch e in the first block of e+2: an epoch stays open to reports through the next one (chain/x/relay/types/limits.go, ReportWindowEpochs). */
const REPORT_WINDOW_EPOCHS = 1;
const FINGERPRINT_BYTES = 20;
const MAX_PREFIX = 32;

function fingerprintHex(value: unknown): string {
  let binary: string;
  try {
    binary = atob(str(value, "relay fingerprint", 64));
  } catch {
    throw new ChainReadError("The chain sent a malformed relay fingerprint.");
  }
  if (binary.length !== FINGERPRINT_BYTES) throw new ChainReadError("The chain sent a malformed relay fingerprint.");
  return Array.from(binary, (c) => c.charCodeAt(0).toString(16).padStart(2, "0")).join("").toUpperCase();
}

export function readRelay(raw: unknown): TorRelay {
  const r = rec(raw, "relay");
  const nodeId = str(r.node_id, "relay node", 64);
  if (!NODE_ID.test(nodeId)) throw new ChainReadError("The chain sent a malformed relay node.");
  return {
    fingerprint: fingerprintHex(r.rsa_fingerprint),
    nodeId,
    operator: account(r.operator, "relay operator"),
    prefix16: clean(str(r.prefix16, "relay network", MAX_PREFIX), MAX_PREFIX),
    exit: bool(r.exit, "relay exit flag"),
    jailed: bool(r.jailed, "relay jail flag"),
  };
}

function readRelayParams(raw: unknown): RelayParams {
  const p = rec(rec(raw, "relay parameters").params, "relay parameters");
  return {
    reportersQuorum: uint(p.min_reporters_quorum, "reporters quorum"),
    minUptime: legacyDec(p.min_uptime_fraction, "minimum uptime"),
    exitMultiplier: legacyDec(p.exit_multiplier, "exit multiplier"),
    perRelayCap: digits(p.per_relay_cap, "per-relay cap"),
    perOperatorCap: digits(p.per_operator_cap, "per-operator cap"),
    perPrefix16Cap: digits(p.per_prefix16_cap, "per-/16 cap"),
  };
}

function readEpochResult(raw: unknown): RelayEpoch {
  const r = rec(rec(raw, "relay epoch").result, "relay epoch");
  return {
    epoch: uint(r.epoch, "relay epoch"),
    quorumMet: bool(r.quorum_met, "relay quorum flag"),
    activating: bool(r.activating, "relay activation flag"),
    ceiling: digits(r.ceiling, "relay ceiling"),
    minted: digits(r.minted, "relay minted"),
  };
}

/** The settlement of an epoch; null when the chain holds none (rewards not on, or nobody reported on it). */
async function readSettlement(client: ChainClient, epoch: number): Promise<RelayEpoch | null> {
  if (epoch < 1) return null;
  try {
    return readEpochResult(await client.get(withQuery(EPOCH_RESULT, { json: JSON.stringify({ epoch: String(epoch) }) })));
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
}

function readPayout(raw: unknown): [string, Norama] {
  const r = rec(raw, "relay payout");
  return [fingerprintHex(r.rsa_fingerprint), digits(r.amount, "relay payout")];
}

/** What a settled epoch paid each relay, by fingerprint; empty when it minted nothing. */
async function readPayouts(client: ChainClient, settlement: RelayEpoch | null): Promise<ReadonlyMap<string, Norama>> {
  if (settlement === null || settlement.minted === "0") return new Map();
  const listed = await listAll(client, RELAY_PAYOUTS, "payouts", readPayout, { maxPages: RELAY_PAGES, request: { epoch: String(settlement.epoch) } });
  return new Map(listed.items);
}

/** Every relay x/relay holds, its parameters and reporters, and the settlement of the newest epoch past its report window. */
export async function loadTorRelays(client: ChainClient): Promise<TorRelays> {
  const [listed, paramsRaw, reportersRaw, epochRaw] = await Promise.all([
    listAll(client, RELAYS, "relays", readRelay, { maxPages: RELAY_PAGES }),
    client.get(RELAY_PARAMS),
    client.get(REPORTERS),
    client.get(CURRENT_EPOCH),
  ]);
  const current = uint(rec(rec(epochRaw, "epoch").epoch_state, "epoch").current_epoch, "epoch number");
  const settlementEpoch = current - 1 - REPORT_WINDOW_EPOCHS;
  const lastSettlement = await readSettlement(client, settlementEpoch);
  return {
    relays: listed.items,
    truncated: listed.more,
    params: readRelayParams(paramsRaw),
    reporters: optArr(rec(reportersRaw, "relay reporters").reporters, "relay reporters").map((r) => account(r, "relay reporter")),
    lastSettlement,
    payouts: await readPayouts(client, lastSettlement),
    settlementEpoch,
  };
}
