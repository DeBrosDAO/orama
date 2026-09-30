import { describe, expect, it } from "vitest";
import { activityLabel, groupByDay, partsToText } from "./activity-model";
import { ALICE, BOB, ME, NOW, sendItem } from "./fixtures";
import type { ActivityItem } from "../../model/types";

const at = (msAgo: number) => new Date(NOW - msAgo).toISOString();
const DAY = 86_400_000;
const text = (i: ActivityItem) => partsToText(activityLabel(i));
const validator = { moniker: "val-3", operator: "orama1val" };

describe("groupByDay", () => {
  it("TestGroupByDay_empty", () => {
    expect(groupByDay([], NOW)).toEqual([]);
  });

  it("TestGroupByDay_groups_by_utc_day_with_headings", () => {
    const items = [sendItem({ time: at(60_000) }), sendItem({ time: at(120_000) }), sendItem({ time: at(DAY) }), sendItem({ time: at(5 * DAY) })];
    const g = groupByDay(items, NOW);
    expect(g.map((x) => x.heading)).toEqual(["Today", "Yesterday", "24 Sep"]);
    expect(g.map((x) => x.items.length)).toEqual([2, 1, 1]);
  });

  it("TestGroupByDay_keeps_order_within_and_across_groups", () => {
    const a = sendItem({ hash: "A", time: at(1000) });
    const b = sendItem({ hash: "B", time: at(2000) });
    const g = groupByDay([a, b], NOW);
    expect(g[0]?.items.map((i) => i.hash)).toEqual(["A", "B"]);
  });

  it("TestGroupByDay_days_are_unique_keys", () => {
    const g = groupByDay([sendItem({ time: at(0) }), sendItem({ time: at(DAY) })], NOW);
    expect(new Set(g.map((x) => x.day)).size).toBe(2);
  });
});

describe("activityLabel", () => {
  it("TestActivityLabel_sent", () => {
    expect(text(sendItem())).toBe("Sent to Alice");
  });

  it("TestActivityLabel_received", () => {
    const item = sendItem({ direction: "in", amount: "5", message: { type: "send", from: BOB, to: ME, amount: "5" } });
    expect(text(item)).toBe("Received from orama1bob");
  });

  it("TestActivityLabel_failed_send_names_the_reason", () => {
    expect(text(sendItem({ status: { ok: false, reason: "out of gas" } }))).toBe("Tried to send to Alice · out of gas");
  });

  it("TestActivityLabel_delegate_undelegate_and_reward", () => {
    const base = { direction: "out" as const, amount: "5", counterparty: null };
    const m = (type: "delegate" | "undelegate" | "claim_rewards") => ({ type, delegator: ME, validator, amount: "5" });
    expect(text(sendItem({ ...base, message: m("delegate") }))).toBe("Delegated to val-3");
    expect(text(sendItem({ ...base, message: m("undelegate") }))).toBe("Started unstaking from val-3");
    expect(text(sendItem({ ...base, direction: "in", message: m("claim_rewards") }))).toBe("Reward from val-3");
  });

  it("TestActivityLabel_storage_deal_both_sides", () => {
    const message = { type: "storage_deal" as const, owner: ME, provider: ALICE, amount: "5", replicas: 3, visibility: "private" as const };
    expect(text(sendItem({ message }))).toBe("Opened a private storage deal · Alice");
    expect(text(sendItem({ message, direction: "in" }))).toBe("Accepted a private storage deal from orama1me");
  });

  it("TestActivityLabel_failed_storage_deal", () => {
    const message = { type: "storage_deal" as const, owner: ME, provider: ALICE, amount: "5", replicas: 1, visibility: "public" as const };
    expect(text(sendItem({ message, status: { ok: false, reason: "no funds" } }))).toBe("Tried to open a public storage deal · Alice · no funds");
  });

  it("TestActivityLabel_unknown_message_is_never_blank", () => {
    const message = { type: "unknown" as const, typeUrl: "/x.Foo", signer: ME };
    expect(text(sendItem({ message }))).toBe("Sent x.Foo message");
  });

  it("TestActivityLabel_names_stay_link_parts", () => {
    const parts = activityLabel(sendItem());
    expect(parts.some((p) => p.kind === "wallet")).toBe(true);
  });
});
