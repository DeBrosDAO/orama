import { describe, expect, it } from "vitest";
import type { ValidatorEpoch } from "../../model/types";
import { capSentence } from "../validators/operators-card";
import { gateValidators, handoverTerms, jailSentence, powerSeries, slashReason, slashTotals, statusWord, windowUptime } from "./logic";

const epoch = (n: number, over: Partial<ValidatorEpoch> = {}): ValidatorEpoch => ({
  epoch: n,
  cometPower: 100 + n,
  signedBlocks: 90,
  missedBlocks: 10,
  uptime: 0.9,
  slashes: 0,
  slashedBurned: "0",
  jailed: false,
  status: "bonded",
  ...over,
});

describe("windowUptime", () => {
  it("TestWindowUptime_adds_blocks_up_instead_of_averaging_percentages", () => {
    // 100% over 1 block and 0% over 99 blocks is 1%, not 50%.
    const w = windowUptime([epoch(2, { signedBlocks: 1, missedBlocks: 0 }), epoch(1, { signedBlocks: 0, missedBlocks: 99 })]);
    expect(w.uptime).toBeCloseTo(0.01);
    expect(w).toMatchObject({ missedBlocks: 99, signedBlocks: 1, epochs: 2 });
  });

  it("TestWindowUptime_no_commit_in_any_epoch_is_no_uptime_not_zero", () => {
    expect(windowUptime([epoch(1, { signedBlocks: 0, missedBlocks: 0 })]).uptime).toBeNull();
    expect(windowUptime([])).toEqual({ uptime: null, missedBlocks: 0, signedBlocks: 0, epochs: 0 });
  });
});

describe("powerSeries", () => {
  it("TestPowerSeries_is_oldest_first_whatever_the_input_order_and_does_not_mutate_it", () => {
    const input = [epoch(3), epoch(1), epoch(2)];
    expect(powerSeries(input)).toEqual([101, 102, 103]);
    expect(input.map((e) => e.epoch)).toEqual([3, 1, 2]);
    expect(powerSeries([])).toEqual([]);
  });
});

describe("slashes and jail", () => {
  it("TestSlashTotals_sums_norama_exactly_beyond_a_safe_integer", () => {
    const burned = ["9007199254740993", "7"];
    const t = slashTotals(burned.map((b) => ({ height: 1, time: "", epoch: 1, reason: "", power: 1, burned: b })));
    expect(t).toEqual({ count: 2, burned: "9007199254741000" });
    expect(slashTotals([])).toEqual({ count: 0, burned: "0" });
  });

  it("TestSlashReason_names_the_reasons_x_slashing_writes_and_shows_any_other_as_written", () => {
    expect(slashReason("double_sign")).toContain("two blocks");
    expect(slashReason("missing_signature")).toContain("Missed");
    expect(slashReason("unspecified")).toBe("Reason not stated");
    expect(slashReason("")).toBe("Reason not stated");
    expect(slashReason("something_new")).toBe("something_new");
  });

  it("TestJailSentence_says_when_a_period_began_and_whether_it_has_ended", () => {
    const base = { jailedTime: null, reason: "", unjailedTime: null };
    expect(jailSentence({ ...base, jailedHeight: 1200, unjailedHeight: 1500 })).toBe("Jailed at block 1,200, until block 1,500.");
    expect(jailSentence({ ...base, jailedHeight: 1200, unjailedHeight: null })).toBe("Jailed at block 1,200, and is still jailed.");
    expect(jailSentence({ ...base, jailedHeight: null, unjailedHeight: 40 })).toBe("Jailed before the index began, until block 40.");
  });

  it("TestStatusWord_is_plain_language", () => {
    expect([statusWord("bonded"), statusWord("unbonding"), statusWord("unbonded")]).toEqual(["Active", "Unbonding", "Not active"]);
  });
});

describe("the hand-over figures", () => {
  it("TestHandoverTerms_are_each_bounded_by_one", () => {
    expect(handoverTerms("10000000000000", "271000000000000", 7, 365)).toEqual({ stake: 0.036900, time: 7 / 365 });
    expect(handoverTerms("999", "100", 400, 365)).toEqual({ stake: 1, time: 1 });
  });

  it("TestHandoverTerms_a_zero_threshold_is_already_met_rather_than_a_division_by_zero", () => {
    expect(handoverTerms("0", "0", 0, 0)).toEqual({ stake: 1, time: 1 });
  });

  it("TestHandoverTerms_nothing_bonded_at_epoch_zero_is_zero", () => {
    expect(handoverTerms("0", "100", 0, 365)).toEqual({ stake: 0, time: 0 });
  });

  it("TestGateValidators_is_twice_the_validators_one_cap_allows", () => {
    expect(gateValidators(0.05)).toBe(40);
    expect(gateValidators(0.03)).toBe(68);
    expect(gateValidators(0)).toBe(0);
  });

  it("TestCapSentence_states_both_caps_and_the_step", () => {
    const s = capSentence({ current: 0.05, normal: 0.05, reduced: 0.03, stepDownCount: 60 });
    expect(s).toContain("5.0%");
    expect(s).toContain("3.0%");
    expect(s).toContain("60 validators or fewer");
  });
});
