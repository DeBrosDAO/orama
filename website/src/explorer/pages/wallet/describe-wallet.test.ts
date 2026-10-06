import { describe, expect, it } from "vitest";
import { partsToText } from "./activity-model";
import { describeWallet } from "./describe-wallet";
import { ALICE, NOW, profile } from "./fixtures";

const words = (p = profile(), now = NOW) => partsToText(describeWallet(p, now));

describe("describeWallet", () => {
  it("TestDescribeWallet_plain_wallet", () => {
    expect(words()).toBe("Last active 2 min ago, first seen 12 d ago, with 128 transactions and no failed payments this week.");
  });

  it("TestDescribeWallet_validator_operator_opening", () => {
    expect(words(profile({}, ["Validator operator"]))).toMatch(/^A validator operator wallet\. /);
  });

  it("TestDescribeWallet_uses_an_before_a_vowel_and_joins_roles", () => {
    expect(words(profile({}, ["Foundation", "Storage provider"]))).toMatch(/^A foundation and storage provider wallet\. /);
    expect(words(profile({}, ["Authority"]))).toMatch(/^An authority wallet\. /);
  });

  it("TestDescribeWallet_sender_and_delegate", () => {
    const p = profile({ topSender: ALICE, topDelegate: { moniker: "val-3", operator: "orama1val" } });
    expect(words(p)).toMatch(/^Mostly receives ORAMA from Alice and delegates to val-3\. Last active/);
  });

  it("TestDescribeWallet_only_sender", () => {
    expect(words(profile({ topSender: ALICE }))).toMatch(/^Mostly receives ORAMA from Alice\. Last active/);
  });

  it("TestDescribeWallet_only_delegate", () => {
    const p = profile({ topDelegate: { moniker: "val-3", operator: "orama1val" } });
    expect(words(p)).toMatch(/^Mostly delegates to val-3\. Last active/);
  });

  it("TestDescribeWallet_no_sender_no_delegate_has_no_mostly", () => {
    expect(words()).not.toContain("Mostly");
  });

  it("TestDescribeWallet_failures_singular_and_plural", () => {
    expect(words(profile({ failedLast7d: 1 }))).toContain("and 1 failed payment this week.");
    expect(words(profile({ failedLast7d: 3 }))).toContain("and 3 failed payments this week.");
    expect(words(profile({ failedLast7d: 0 }))).toContain("no failed payments this week.");
  });

  it("TestDescribeWallet_transaction_count_singular_plural_and_grouping", () => {
    expect(words(profile({ txCount: 1 }))).toContain("with 1 transaction and");
    expect(words(profile({ txCount: 0 }))).toContain("with 0 transactions and");
    expect(words(profile({ txCount: 12345 }))).toContain("with 12,345 transactions and");
  });

  it("TestDescribeWallet_keeps_names_as_link_parts", () => {
    const parts = describeWallet(profile({ topSender: ALICE }), NOW);
    expect(parts.some((p) => p.kind === "wallet" && p.ref.address === ALICE.address)).toBe(true);
  });

  it("TestDescribeWallet_old_activity_shows_a_date", () => {
    const old = profile({ lastActive: "2026-01-05T10:00:00.000Z" });
    expect(words(old)).toContain("Last active 5 Jan 2026,");
  });
});
