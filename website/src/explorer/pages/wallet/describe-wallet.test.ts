import { describe, expect, it } from "vitest";
import { partsToText } from "./activity-model";
import { describeWallet } from "./describe-wallet";
import { NOW, profile } from "./fixtures";

const words = (p = profile(), now = NOW) => partsToText(describeWallet(p, now));

describe("describeWallet", () => {
  it("TestDescribeWallet_plain_wallet", () => {
    expect(words()).toBe("Last active 2 min ago, first seen 12 d ago, with 128 transactions.");
  });

  it("TestDescribeWallet_validator_operator_opening", () => {
    expect(words(profile({}, ["Validator operator"]))).toMatch(/^A validator operator wallet\. /);
  });

  it("TestDescribeWallet_uses_an_before_a_vowel_and_joins_roles", () => {
    expect(words(profile({}, ["Foundation", "Storage provider"]))).toMatch(/^A foundation and storage provider wallet\. /);
    expect(words(profile({}, ["Authority"]))).toMatch(/^An authority wallet\. /);
  });

  it("TestDescribeWallet_transaction_count_singular_plural_and_grouping", () => {
    expect(words(profile({ txCount: 1 }))).toContain("with 1 transaction.");
    expect(words(profile({ txCount: 12345 }))).toContain("with 12,345 transactions.");
  });

  it("TestDescribeWallet_old_activity_shows_a_date", () => {
    const old = profile({ lastActive: "2026-01-05T10:00:00.000Z" });
    expect(words(old)).toContain("Last active 5 Jan 2026,");
  });

  it("TestDescribeWallet_a_funded_wallet_with_no_transaction_says_so", () => {
    const funded = profile({ firstSeen: null, lastActive: null, txCount: 0 });
    expect(words(funded)).toBe("No transaction has named this wallet yet.");
    expect(words(profile({ firstSeen: null, lastActive: null, txCount: 0 }, ["Validator operator"]))).toBe(
      "A validator operator wallet. No transaction has named this wallet yet.",
    );
  });
});
