import { describe, expect, it } from "vitest";
import type { Validator, ValidatorSet } from "../../model/types";
import { handoverSentence, rankValidators, summarize, uptimeLabel } from "./summary";

const v = (moniker: string, type: Validator["type"], power: number, jailed = false): Validator => ({
  ref: { moniker, operator: `orama1${moniker}` },
  type,
  power,
  jailed,
  uptimeDays: [],
  uptimePct: 100,
});

const set = (validators: Validator[], lambda: number): ValidatorSet => ({
  lambda,
  nakamoto: 1,
  totalStaked: "0",
  delegators: 0,
  jailedLast30d: 0,
  validators,
});

describe("summarize", () => {
  it("TestSummarize_splits_power_between_committee_and_community", () => {
    const s = summarize(set([v("a", "committee", 0.82), v("b", "community", 0.18)], 0.18));
    expect(s.committeeShare).toBeCloseTo(0.82);
    expect(s.communityShare).toBe(0.18);
    expect(s.signing).toBe(2);
    expect(s.total).toBe(2);
  });

  it("TestSummarize_counts_jailed_validators_as_not_signing", () => {
    const s = summarize(set([v("a", "committee", 1), v("b", "community", 0, true)], 0));
    expect(s.signing).toBe(1);
    expect(s.total).toBe(2);
  });

  it("TestSummarize_empty_set", () => {
    const s = summarize(set([], 0));
    expect(s).toEqual({ committeeShare: 0, communityShare: 0, signing: 0, total: 0 });
  });
});

describe("rankValidators", () => {
  it("TestRankValidators_orders_by_power_then_puts_jailed_last", () => {
    const ranked = rankValidators([v("z", "community", 0.1), v("j", "community", 0.5, true), v("a", "committee", 0.4), v("b", "committee", 0.1)]);
    expect(ranked.map((x) => x.ref.moniker)).toEqual(["a", "b", "z", "j"]);
  });

  it("TestRankValidators_does_not_mutate_its_input", () => {
    const input = [v("b", "committee", 0.1), v("a", "committee", 0.9)];
    rankValidators(input);
    expect(input.map((x) => x.ref.moniker)).toEqual(["b", "a"]);
  });
});

describe("uptimeLabel", () => {
  it("TestUptimeLabel_counts_each_kind", () => {
    expect(uptimeLabel(["ok", "ok", "partial", "missed"])).toBe("2 of 4 days fully up, 1 partial, 1 missed");
  });

  it("TestUptimeLabel_empty", () => {
    expect(uptimeLabel([])).toBe("0 of 0 days fully up, 0 partial, 0 missed");
  });
});

describe("handoverSentence", () => {
  it("TestHandoverSentence_rounds_to_a_whole_percent", () => {
    expect(handoverSentence({ committeeShare: 0.824, communityShare: 0.176, signing: 1, total: 1 })).toBe(
      "Today, 82% of voting power sits with the founding committee.",
    );
  });

  it("TestHandoverSentence_boundaries", () => {
    expect(handoverSentence({ committeeShare: 1, communityShare: 0, signing: 1, total: 1 })).toContain("All voting power");
    expect(handoverSentence({ committeeShare: 0, communityShare: 1, signing: 1, total: 1 })).toContain("No voting power");
  });
});
