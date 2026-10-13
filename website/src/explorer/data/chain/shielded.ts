import type { KeyQuery } from "../../model/paging";
import { SHIELDED_POOL_ADDRESS, bundleOf } from "../../model/shielded";
import type { ShieldedActivity, ShieldedParams, ShieldedPool, ShieldedTx } from "../../model/shielded";
import { INDEX_MAX_LIMIT } from "./aggregates";
import { ChainNotFound, chainPath, withQuery } from "./client";
import type { ChainClient } from "./client";
import { queryPath } from "./module-query";
import { nextCursor } from "./paging";
import { readIndexTx } from "./tx";
import { digits, optArr, optUint, rec, uint } from "./wire";

const PARAMS = "orama.shielded.v1.Query/Params";
const TREE_STATE = "orama.shielded.v1.Query/TreeState";
const BALANCE = "cosmos.bank.v1beta1.Query/Balance";
const NORAMA = "norama";

function readParams(raw: unknown): ShieldedParams {
  const p = rec(raw, "shielded parameters");
  return {
    nullifierFee: digits(p.nullifier_fee, "nullifier fee"),
    maxActionsPerBundle: uint(p.max_actions_per_bundle, "bundle action limit"),
    unshieldFloor: digits(p.unshield_floor, "unshield floor"),
    maxFeeTopup: digits(p.max_fee_topup, "fee top-up limit"),
  };
}

/**
 * What the chain exposes of the shielded pool as a whole: its parameters, the size of its note tree
 * and nullifier set (x/shielded's TreeState), and what the pool's module account holds. x/shielded's
 * own Pools query, which would split the pool from the unshield queue, is withheld by the gateway.
 */
export async function loadShieldedPool(client: ChainClient): Promise<ShieldedPool> {
  const [params, tree, balance] = await Promise.all([
    client.get(queryPath(PARAMS)),
    client.get(queryPath(TREE_STATE)),
    client.get(queryPath(BALANCE, { address: SHIELDED_POOL_ADDRESS, denom: NORAMA })),
  ]);
  const state = rec(tree, "shielded tree state");
  return {
    held: digits(rec(rec(balance, "pool balance").balance, "pool balance").amount, "pool balance"),
    noteCommitments: digits(state.tree_size ?? "0", "note commitment count"),
    nullifiers: digits(state.nullifier_count ?? "0", "nullifier count"),
    params: readParams(rec(params, "shielded parameters").params),
  };
}

function readShieldedTx(raw: unknown): ShieldedTx {
  const tx = readIndexTx(raw);
  return {
    hash: tx.hash,
    height: tx.height,
    time: tx.time,
    ok: tx.code === 0,
    bundle: tx.code === 0 ? bundleOf(tx.events) : null,
    signer: tx.signer === null ? null : { address: tx.signer },
  };
}

/** How many transactions named the pool's account since the index began; null when the index has none for it. */
export async function loadShieldedActivityTotal(client: ChainClient): Promise<number | null> {
  try {
    return optUint(rec(await client.get(chainPath("index", "accounts", SHIELDED_POOL_ADDRESS)), "pool account").tx_count, "pool transaction count");
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
}

/**
 * The transactions that moved coins into or out of the pool's account, newest first: every shield,
 * shield-earnings and unshield, and every private transfer (which burns its fee from the pool). The
 * index lists the transactions whose events name an account, and these name the pool's.
 */
export async function loadShieldedActivity(client: ChainClient, query: KeyQuery): Promise<ShieldedActivity> {
  const limit = Math.min(INDEX_MAX_LIMIT, Math.max(1, Math.floor(query.limit)));
  const where = query.key === null ? { page: 1, limit } : { cursor: query.key, limit };
  const path = withQuery(chainPath("index", "accounts", SHIELDED_POOL_ADDRESS, "txs"), where);
  const answer = rec(await client.get(path), "pool transactions");
  const items = optArr(answer.txs, "pool transactions").map(readShieldedTx);
  return { items, nextKey: nextCursor(answer, items.length, limit) };
}

