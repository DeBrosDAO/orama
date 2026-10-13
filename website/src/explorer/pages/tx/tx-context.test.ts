import { describe, expect, it } from "vitest";
import type { TxContext, TxMessage } from "../../model/types";
import { describeContext } from "./tx-context";

const alice = { address: "orama1alice" };
const bob = { address: "orama1bob" };
const send: TxMessage = { type: "send", from: alice, to: bob, amount: "12500000000" };
const delegate: TxMessage = {
  type: "delegate",
  delegator: alice,
  validator: { moniker: "v1", operator: "orama1v1" },
  amount: "1",
};

const ctx = (over: Partial<TxContext> = {}): TxContext => ({
  amountPercentile: null,
  priorBetweenParties: null,
  previousFromSigner: null,
  otherInBlock: 0,
  ...over,
});

describe("describeContext", () => {
  it("TestDescribeContext_percentile_and_repeat_payer", () => {
    expect(describeContext(ctx({ amountPercentile: 71, priorBetweenParties: 2 }), send)).toEqual([
      "A transfer this size is larger than 71% of transfers this week.",
      "The sender has paid this receiver before (2 times).",
    ]);
  });

  it("TestDescribeContext_first_payment_between_parties", () => {
    expect(describeContext(ctx({ priorBetweenParties: 0 }), send)).toEqual([
      "This is the first time the sender has paid this receiver.",
    ]);
  });

  it("TestDescribeContext_one_prior_payment_is_singular", () => {
    expect(describeContext(ctx({ priorBetweenParties: 1 }), send)).toEqual([
      "The sender has paid this receiver before (1 time).",
    ]);
  });

  it("TestDescribeContext_no_facts_gives_empty_list", () => {
    expect(describeContext(ctx({ previousFromSigner: null, otherInBlock: 5 }), send)).toEqual([]);
  });

  it("TestDescribeContext_non_transfer_gives_empty_list", () => {
    expect(describeContext(ctx({ amountPercentile: 50, priorBetweenParties: 3 }), delegate)).toEqual([]);
  });

  it("TestDescribeContext_smallest_percentile", () => {
    expect(describeContext(ctx({ amountPercentile: 0 }), send)).toEqual([
      "A transfer this size is among the smallest transfers this week.",
    ]);
  });

  it("TestDescribeContext_percentile_is_clamped_and_rounded", () => {
    expect(describeContext(ctx({ amountPercentile: 140 }), send)[0]).toContain("larger than 100%");
    expect(describeContext(ctx({ amountPercentile: 70.6 }), send)[0]).toContain("larger than 71%");
    expect(describeContext(ctx({ amountPercentile: -5 }), send)[0]).toContain("smallest");
  });

  it("TestDescribeContext_ignores_nonsense_numbers", () => {
    expect(describeContext(ctx({ amountPercentile: Number.NaN, priorBetweenParties: -1 }), send)).toEqual([]);
    expect(describeContext(ctx({ priorBetweenParties: 1.5 }), send)).toEqual([]);
  });
});
