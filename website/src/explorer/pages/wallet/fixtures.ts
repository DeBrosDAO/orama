import type { ActivityItem, Counterparty, WalletProfile, WalletRef } from "../../model/types";

export const NOW = Date.UTC(2026, 8, 29, 14, 0, 0);

export const ALICE: WalletRef = { address: "orama1alice", label: "Alice" };
export const BOB: WalletRef = { address: "orama1bob" };
export const ME: WalletRef = { address: "orama1me" };

export function profile(over: Partial<WalletProfile["facts"]> = {}, roles: string[] = []): WalletProfile {
  return {
    ref: ME,
    roles,
    balance: { available: "0", staked: "0", unbonding: "0", total: "0" },
    facts: {
      firstSeen: new Date(NOW - 12 * 86_400_000).toISOString(),
      lastActive: new Date(NOW - 2 * 60_000).toISOString(),
      txCount: 128,
      ...over,
    },
  };
}

export function sendItem(over: Partial<ActivityItem> = {}): ActivityItem {
  return {
    hash: "A".repeat(64),
    time: new Date(NOW - 60_000).toISOString(),
    status: { ok: true },
    message: { type: "send", from: ME, to: ALICE, amount: "12500000000" },
    direction: "out",
    amount: "-12500000000",
    counterparty: ALICE,
    ...over,
  };
}

export function counterparty(volume: string, address = "orama1x"): Counterparty {
  return { ref: { address }, txCount: 1, net: "0", volume };
}
