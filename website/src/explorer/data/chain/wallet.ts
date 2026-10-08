import { categoryOf } from "../../model/describe";
import type {
  ActivityDirection,
  ActivityItem,
  ActivityQuery,
  Counterparty,
  Page,
  SignedNorama,
  TxMessage,
  WalletProfile,
  WalletRef,
} from "../../model/types";
import { ChainNotFound, ChainReadError, chainPath, withQuery } from "./client";
import type { ChainClient } from "./client";
import type { Directory } from "./directory";
import { readIndexTx, summaryOf, walletRef } from "./tx";
import type { Context, IndexTx } from "./tx";
import { arr, digits, iso, optArr, optDigits, rec, uint } from "./wire";

const NORAMA = "norama";
/** The indexer's page size for an account's transactions, and the most pages one activity call reads. */
const INDEX_PAGE_SIZE = 100;
const MAX_SCAN_PAGES = 5;
const CURSOR = /^([1-9][0-9]{0,3}):([0-9]{1,3})$/;
/** Counterparties are read from this many of the wallet's newest transactions. */
const COUNTERPARTY_WINDOW = 100;

function noramaOf(coin: unknown, what: string): bigint {
  const c = rec(coin, what);
  return c.denom === NORAMA ? BigInt(digits(c.amount, what)) : 0n;
}

async function bankBalance(client: ChainClient, address: string): Promise<bigint> {
  const body = rec(await client.get(chainPath("bank", "balances", address)), "balance");
  return optArr(body.balances, "balance").reduce<bigint>((n, c) => n + noramaOf(c, "balance"), 0n);
}

async function stakedBalance(client: ChainClient, address: string): Promise<bigint> {
  const body = rec(await client.get(chainPath("staking", "delegations", address)), "delegations");
  return optArr(body.delegation_responses, "delegations").reduce<bigint>(
    (n, d) => n + noramaOf(rec(d, "delegation").balance, "delegation balance"),
    0n,
  );
}

async function unbondingBalance(client: ChainClient, address: string): Promise<bigint> {
  const body = rec(await client.get(chainPath("staking", "unbonding", address)), "unbonding");
  let total = 0n;
  for (const u of optArr(body.unbonding_responses, "unbonding")) {
    for (const entry of optArr(rec(u, "unbonding delegation").entries, "unbonding entries")) {
      total += BigInt(optDigits(rec(entry, "unbonding entry").balance, "unbonding balance"));
    }
  }
  return total;
}

interface AccountSummary {
  txCount: number;
  firstSeen: string;
  lastActive: string;
}

async function accountSummary(client: ChainClient, address: string): Promise<AccountSummary | null> {
  try {
    const s = rec(await client.get(chainPath("index", "accounts", address)), "account summary");
    return { txCount: uint(s.tx_count, "transaction count"), firstSeen: iso(s.first_seen, "first seen"), lastActive: iso(s.last_active, "last active") };
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
}

/** A wallet's balance split and facts, or null when it holds nothing and no transaction names it. */
export async function loadWallet(client: ChainClient, directory: Directory, address: string, ctx: Context): Promise<WalletProfile | null> {
  const [available, staked, unbonding, summary] = await Promise.all([
    bankBalance(client, address),
    stakedBalance(client, address),
    unbondingBalance(client, address),
    accountSummary(client, address),
  ]);
  const total = available + staked + unbonding;
  if (summary === null && total === 0n) return null;
  const validator = directory.byOperator.get(address);
  const roles = [...(validator ? ["Validator operator"] : []), ...(validator?.committee ? ["Founding committee"] : [])];
  return {
    ref: walletRef(address, ctx),
    roles,
    balance: { available: available.toString(), staked: staked.toString(), unbonding: unbonding.toString(), total: total.toString() },
    facts: { firstSeen: summary?.firstSeen ?? null, lastActive: summary?.lastActive ?? null, txCount: summary?.txCount ?? 0 },
  };
}

function involves(m: TxMessage, address: string): boolean {
  switch (m.type) {
    case "send":
      return m.from.address === address || m.to.address === address;
    case "delegate":
    case "undelegate":
    case "claim_rewards":
      return m.delegator.address === address;
    case "storage_deal":
      return m.owner.address === address;
    case "unknown":
      return m.signer?.address === address;
  }
}

interface Side {
  direction: ActivityDirection;
  amount: SignedNorama;
  counterparty: WalletRef | null;
}

/** What a message did, from one wallet's side. */
function sideOf(m: TxMessage, address: string): Side {
  switch (m.type) {
    case "send":
      return m.from.address === address
        ? { direction: "out", amount: `-${m.amount}`, counterparty: m.to }
        : { direction: "in", amount: m.amount, counterparty: m.from };
    case "delegate":
    case "storage_deal":
      return { direction: "out", amount: `-${m.amount}`, counterparty: null };
    case "undelegate":
    case "claim_rewards":
      return { direction: "in", amount: m.amount, counterparty: null };
    case "unknown":
      return { direction: m.signer?.address === address ? "out" : "in", amount: "0", counterparty: null };
  }
}

function itemFor(tx: IndexTx, ctx: Context, address: string): ActivityItem {
  const summary = summaryOf(tx, ctx);
  const own = summary.messages.find((m) => involves(m, address));
  // A transaction that names the wallet only in its events (a payout to it) shows its first message, seen from outside.
  const message: TxMessage = own ?? summary.messages[0] ?? { type: "unknown", typeUrl: "(no message)", signer: summary.signer };
  const side = own
    ? sideOf(own, address)
    : { direction: (summary.signer?.address === address ? "out" : "in") as ActivityDirection, amount: "0", counterparty: null };
  return { hash: summary.hash, time: summary.time, status: summary.status, message, ...side };
}

function matches(item: ActivityItem, query: ActivityQuery): boolean {
  if (query.counterparty !== null && item.counterparty?.address !== query.counterparty) return false;
  switch (query.filter) {
    case "all":
      return true;
    case "in":
    case "out":
      return item.direction === query.filter;
    case "staking":
    case "storage":
      return categoryOf(item.message) === query.filter;
    case "failed":
      return !item.status.ok;
  }
}

function parseCursor(cursor: string | null): { page: number; skip: number } {
  if (cursor === null) return { page: 1, skip: 0 };
  const m = CURSOR.exec(cursor);
  if (!m) throw new ChainReadError("That page of activity was not issued by this explorer.");
  return { page: Number(m[1]), skip: Number(m[2]) };
}

async function accountTxs(client: ChainClient, address: string, page: number, limit: number): Promise<IndexTx[]> {
  const path = withQuery(chainPath("index", "accounts", address, "txs"), { page, limit });
  return arr(rec(await client.get(path), "account transactions").txs, "account transactions").map(readIndexTx);
}

/**
 * A wallet's activity newest first, `query.limit` rows at a time. The indexer
 * is read a page of 100 at a time and filtered here, so a narrow filter can
 * read several pages for one answer; the cursor is "page:position".
 */
export async function loadActivity(client: ChainClient, ctx: Context, address: string, query: ActivityQuery): Promise<Page<ActivityItem>> {
  const wanted = query.limit;
  const items: ActivityItem[] = [];
  let { page, skip } = parseCursor(query.cursor);
  for (let scanned = 0; scanned < MAX_SCAN_PAGES; scanned++) {
    const txs = await accountTxs(client, address, page, INDEX_PAGE_SIZE);
    for (let i = skip; i < txs.length; i++) {
      const tx = txs[i];
      if (tx === undefined) continue;
      const item = itemFor(tx, ctx, address);
      if (!matches(item, query)) continue;
      items.push(item);
      if (items.length === wanted) return { items, nextCursor: positionAfter(page, i, txs.length) };
    }
    if (txs.length < INDEX_PAGE_SIZE) return { items, nextCursor: null };
    page += 1;
    skip = 0;
  }
  return { items, nextCursor: `${page}:0` };
}

/** The cursor of the row after the one at `index` of a page of `length`, or null at the end of the account's history. */
function positionAfter(page: number, index: number, length: number): string | null {
  if (index + 1 < length) return `${page}:${index + 1}`;
  return length < INDEX_PAGE_SIZE ? null : `${page + 1}:0`;
}

/** The wallets this one sent to or received from most, by volume, from its newest transactions. */
export async function loadCounterparties(client: ChainClient, ctx: Context, address: string, limit: number): Promise<Counterparty[]> {
  const txs = await accountTxs(client, address, 1, COUNTERPARTY_WINDOW);
  const byWallet = new Map<string, { ref: WalletRef; txCount: number; net: bigint; volume: bigint }>();
  for (const tx of txs) {
    const item = itemFor(tx, ctx, address);
    if (!item.status.ok || item.message.type !== "send" || item.counterparty === null) continue;
    const amount = BigInt(item.amount);
    const row = byWallet.get(item.counterparty.address) ?? { ref: item.counterparty, txCount: 0, net: 0n, volume: 0n };
    row.txCount += 1;
    row.net += amount;
    row.volume += amount < 0n ? -amount : amount;
    byWallet.set(item.counterparty.address, row);
  }
  return [...byWallet.values()]
    .sort((a, b) => (a.volume === b.volume ? a.ref.address.localeCompare(b.ref.address) : a.volume > b.volume ? -1 : 1))
    .slice(0, limit)
    .map((r) => ({ ref: r.ref, txCount: r.txCount, net: r.net.toString(), volume: r.volume.toString() }));
}
