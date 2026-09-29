import type {
  ActivityItem,
  ActivityQuery,
  BalancePoint,
  BalanceRange,
  Counterparty,
  Page,
  TxMessage,
  WalletFilter,
  WalletProfile,
  WalletRef,
} from "../../model/types";
import { categoryOf } from "../../model/describe";
import { TIME } from "../../model/time";
import type { Account } from "./ledger";
import { validatorRef } from "./roster";
import type { TxRecord, World } from "./world";

const MAX_HISTORY_POINTS = 160;
const RECENT_FAILURE_DAYS = 7;
const CURSOR = /^(0|[1-9][0-9]*)$/;

function rolesOf(world: World, a: Account): string[] {
  if (a === world.foundation) return ["Foundation"];
  if (world.providers.includes(a)) return ["Storage provider"];
  if (world.validators.some((v) => v.ref.operator === a.ref.address)) return ["Validator operator"];
  return [];
}

/**
 * One row for the wallet's part in a transaction. Staking rows carry no
 * counterparty: the validator's operator wallet is not a party to them, so its
 * page does not contain the transaction and a link to it would lead nowhere.
 */
function itemFor(address: string, rec: TxRecord): ActivityItem | null {
  const m = rec.summary.messages[0] as TxMessage;
  const base = { hash: rec.summary.hash, time: rec.summary.time, status: rec.summary.status, message: m };
  const amountOf = BigInt("amount" in m ? m.amount : "0");
  switch (m.type) {
    case "send": {
      const out = m.from.address === address;
      return { ...base, direction: out ? "out" : "in", amount: (out ? -amountOf : amountOf).toString(), counterparty: out ? m.to : m.from };
    }
    case "delegate":
      return { ...base, direction: "out", amount: (-amountOf).toString(), counterparty: null };
    case "storage_deal":
      return { ...base, direction: "out", amount: (-amountOf).toString(), counterparty: m.provider };
    case "undelegate":
    case "claim_rewards":
      return { ...base, direction: "in", amount: amountOf.toString(), counterparty: null };
    case "unknown":
      return null;
  }
}

function allows(item: ActivityItem, filter: WalletFilter): boolean {
  const ok = item.status.ok;
  switch (filter) {
    case "all":
      return true;
    case "failed":
      return !ok;
    case "in":
      return ok && item.direction === "in";
    case "out":
      return ok && item.direction === "out";
    case "staking":
      return ok && categoryOf(item.message) === "staking";
    case "storage":
      return ok && categoryOf(item.message) === "storage";
  }
}

function allItems(world: World, a: Account): ActivityItem[] {
  const out: ActivityItem[] = [];
  for (let p = a.txIndices.length - 1; p >= 0; p--) {
    const item = itemFor(a.ref.address, world.txs[a.txIndices[p] as number] as TxRecord);
    if (item) out.push(item);
  }
  return out;
}

export function walletProfile(world: World, address: string, nowMs: number): WalletProfile | null {
  const a = world.account(address);
  if (!a || a.txIndices.length === 0) return null;
  let available = a.available;
  let unbonding = 0n;
  for (const u of a.unbonding) {
    if (u.completesMs <= nowMs) available += u.amount;
    else unbonding += u.amount;
  }
  const staked = a.staked();
  const items = allItems(world, a);
  const last = world.txs[a.txIndices[a.txIndices.length - 1] as number] as TxRecord;
  return {
    ref: a.ref,
    roles: rolesOf(world, a),
    balance: {
      available: available.toString(),
      staked: staked.toString(),
      unbonding: unbonding.toString(),
      claimableRewards: a.claimable(nowMs).toString(),
      total: (available + staked + unbonding).toString(),
    },
    facts: {
      firstSeen: new Date(a.firstSeenMs as number).toISOString(),
      lastActive: last.summary.time,
      txCount: a.txIndices.length,
      failedLast7d: items.filter((i) => !i.status.ok && Date.parse(i.time) >= nowMs - RECENT_FAILURE_DAYS * TIME.DAY).length,
      topSender: topSender(items),
      topDelegate: topDelegate(a),
    },
  };
}

function topSender(items: ActivityItem[]): WalletRef | null {
  const volume = new Map<string, { ref: WalletRef; sum: bigint }>();
  for (const i of items) {
    if (!i.status.ok || i.direction !== "in" || i.message.type !== "send" || !i.counterparty) continue;
    const row = volume.get(i.counterparty.address) ?? { ref: i.counterparty, sum: 0n };
    row.sum += BigInt(i.amount);
    volume.set(i.counterparty.address, row);
  }
  return [...volume.values()].sort((x, y) => (y.sum > x.sum ? 1 : -1))[0]?.ref ?? null;
}

function topDelegate(a: Account) {
  let best: { moniker: string; amount: bigint } | null = null;
  for (const [moniker, s] of a.stakes) {
    if (s.amount > 0n && (!best || s.amount > best.amount)) best = { moniker, amount: s.amount };
  }
  return best ? validatorRef(best.moniker) : null;
}

/** The position a cursor names, or a rejection: cursors are only ever ones this source handed out. */
function startOf(cursor: string | null, length: number): number {
  if (cursor === null) return length - 1;
  const position = CURSOR.test(cursor) ? Number(cursor) : -1;
  if (position < 0 || position >= length) throw new Error("invalid activity cursor");
  return position;
}

export function walletActivity(world: World, address: string, q: ActivityQuery): Page<ActivityItem> {
  const a = world.account(address);
  const start = startOf(q.cursor, a?.txIndices.length ?? 0);
  if (!a) return { items: [], nextCursor: null };
  const items: ActivityItem[] = [];
  let p = start;
  for (; p >= 0 && items.length < q.limit; p--) {
    const item = itemFor(address, world.txs[a.txIndices[p] as number] as TxRecord);
    if (!item || !allows(item, q.filter)) continue;
    if (q.counterparty !== null && item.counterparty?.address !== q.counterparty) continue;
    items.push(item);
  }
  return { items, nextCursor: p >= 0 ? String(p) : null };
}

export function counterparties(world: World, address: string, limit: number): Counterparty[] {
  const a = world.account(address);
  if (!a) return [];
  const rows = new Map<string, { ref: WalletRef; count: number; net: bigint; volume: bigint }>();
  for (const i of allItems(world, a)) {
    if (!i.status.ok || !i.counterparty) continue;
    const row = rows.get(i.counterparty.address) ?? { ref: i.counterparty, count: 0, net: 0n, volume: 0n };
    const amount = BigInt(i.amount);
    row.count++;
    row.net += amount;
    row.volume += amount < 0n ? -amount : amount;
    rows.set(i.counterparty.address, row);
  }
  return [...rows.values()]
    .sort((x, y) => (y.volume > x.volume ? 1 : y.volume < x.volume ? -1 : 0))
    .slice(0, limit)
    .map((r) => ({ ref: r.ref, txCount: r.count, net: r.net.toString(), volume: r.volume.toString() }));
}

export function balanceHistory(world: World, address: string, range: BalanceRange, nowMs: number): BalancePoint[] {
  const a = world.account(address);
  if (!a) return [];
  const days = range === "7d" ? 7 : range === "30d" ? 30 : null;
  const from = days === null ? -Infinity : nowMs - days * TIME.DAY;
  const inRange = a.points.filter((p) => p.timeMs >= from);
  const opening = a.points.filter((p) => p.timeMs < from).at(-1);
  const points = [...(opening && days !== null ? [{ timeMs: from, total: opening.total }] : []), ...inRange];
  points.push({ timeMs: nowMs, total: a.total() });
  const stride = Math.max(1, Math.ceil(points.length / MAX_HISTORY_POINTS));
  return points
    .filter((_, i) => i % stride === 0 || i === points.length - 1)
    .map((p) => ({ time: new Date(p.timeMs).toISOString(), total: p.total.toString() }));
}

export function searchLabels(world: World, query: string, limit: number): WalletRef[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  return [...world.accounts.values()]
    .map((a) => a.ref)
    .filter((r) => r.label?.toLowerCase().includes(q))
    .sort((x, y) => (x.label as string).toLowerCase().indexOf(q) - (y.label as string).toLowerCase().indexOf(q))
    .slice(0, limit);
}
