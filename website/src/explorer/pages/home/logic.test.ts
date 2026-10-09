import { describe, expect, it } from "vitest";
import type { BlockSummary, NetworkSnapshot } from "../../model/types";
import { averageTxs, blockLabel, blockShade, deltaText, epochPercent, formatSeconds, hasSupermajority, healthSentence, TONE_ANNOUNCEMENT } from "./logic";

function network(over: Partial<NetworkSnapshot> = {}): NetworkSnapshot {
  return {
    chainId: "orama-test",
    height: 1284410,
    blockTimeSeconds: 2,
    validatorsSigning: 7,
    validatorsTotal: 7,
    epoch: { number: 12, progress: 0.4, endsAt: "2026-09-29T20:00:00Z" },
    supply: "41200000000000000",
    baseFee: 1000,
    transactions24h: 18204,
    transactionsChangePct: 12,
    transactionsSeries: [1, 2, 3],
    failed24h: 17,
    burned24h: "312000000000",
    ...over,
  };
}

function block(height: number, txCount: number): BlockSummary {
  return { height, hash: "AB", time: "2026-09-29T14:00:00Z", proposer: { moniker: "v", operator: "orama1x" }, txCount };
}

describe("healthSentence", () => {
  it("TestHealthSentence_all_signing_is_healthy", () => {
    expect(healthSentence(network())).toEqual({
      tone: "healthy",
      sentence: "Network healthy · block 1,284,410 · 7 of 7 validators signing · transactions final in ~2 s",
    });
  });

  it("TestHealthSentence_supermajority_short_of_all_stays_healthy_and_honest", () => {
    const h = healthSentence(network({ validatorsSigning: 6, validatorsTotal: 7 }));
    expect(h.tone).toBe("healthy");
    expect(h.sentence).toContain("6 of 7 validators signing");
  });

  it("TestHealthSentence_exactly_two_thirds_is_degraded", () => {
    const h = healthSentence(network({ validatorsSigning: 4, validatorsTotal: 6 }));
    expect(h.tone).toBe("degraded");
    expect(h.sentence.startsWith("Degraded")).toBe(true);
    expect(h.sentence).not.toContain("final in");
  });

  it("TestHealthSentence_all_validators_jailed_is_degraded", () => {
    const h = healthSentence(network({ validatorsSigning: 0, validatorsTotal: 7 }));
    expect(h.tone).toBe("degraded");
    expect(h.sentence).toContain("0 of 7 validators signing");
  });

  it("TestHealthSentence_no_validators_is_degraded", () => {
    expect(healthSentence(network({ validatorsSigning: 0, validatorsTotal: 0 })).tone).toBe("degraded");
  });

  it("TestHealthSentence_fractional_block_time", () => {
    expect(healthSentence(network({ blockTimeSeconds: 1.5 })).sentence).toContain("~1.5 s");
  });
});

describe("hasSupermajority", () => {
  it("TestHasSupermajority_boundary", () => {
    expect(hasSupermajority(2, 3)).toBe(false);
    expect(hasSupermajority(3, 4)).toBe(true);
    expect(hasSupermajority(1, 1)).toBe(true);
    expect(hasSupermajority(0, 0)).toBe(false);
  });
});

describe("formatSeconds", () => {
  it("TestFormatSeconds_whole_and_fraction", () => {
    expect(formatSeconds(2)).toBe("2");
    expect(formatSeconds(2.25)).toBe("2.3");
  });
});

describe("deltaText", () => {
  it("TestDeltaText_up_and_down", () => {
    expect(deltaText(12)).toEqual({ tone: "up", text: "▲ 12%" });
    expect(deltaText(-3.4)).toEqual({ tone: "down", text: "▼ 3%" });
  });

  it("TestDeltaText_null_has_no_text", () => {
    expect(deltaText(null)).toBeNull();
    expect(deltaText(Number.NaN)).toBeNull();
  });

  it("TestDeltaText_rounds_to_zero_is_flat", () => {
    expect(deltaText(0)).toEqual({ tone: "flat", text: "No change" });
    expect(deltaText(0.49)).toEqual({ tone: "flat", text: "No change" });
    expect(deltaText(-0.4)).toEqual({ tone: "flat", text: "No change" });
    expect(deltaText(0.5)?.tone).toBe("up");
  });
});

describe("blockShade", () => {
  it("TestBlockShade_bucket_boundaries", () => {
    expect([0, 1, 2, 3, 5, 6, 40].map(blockShade)).toEqual([0, 1, 1, 2, 2, 3, 3]);
  });

  it("TestBlockShade_negative_is_empty", () => {
    expect(blockShade(-1)).toBe(0);
  });
});

describe("blockLabel", () => {
  it("TestBlockLabel_singular_plural_and_zero", () => {
    expect(blockLabel(block(1284410, 1))).toBe("Block 1,284,410, 1 transaction");
    expect(blockLabel(block(9, 0))).toBe("Block 9, 0 transactions");
    expect(blockLabel(block(9, 7))).toBe("Block 9, 7 transactions");
  });
});

describe("averageTxs", () => {
  it("TestAverageTxs_mean_one_decimal", () => {
    expect(averageTxs([block(1, 3), block(2, 4)])).toBe("3.5");
  });

  it("TestAverageTxs_empty_is_null", () => {
    expect(averageTxs([])).toBeNull();
  });

  it("TestAverageTxs_all_zero", () => {
    expect(averageTxs([block(1, 0), block(2, 0)])).toBe("0.0");
  });
});

describe("epochPercent", () => {
  it("TestEpochPercent_rounds_and_clamps", () => {
    expect(epochPercent(0.4)).toBe(40);
    expect(epochPercent(0)).toBe(0);
    expect(epochPercent(1)).toBe(100);
    expect(epochPercent(1.7)).toBe(100);
    expect(epochPercent(-0.2)).toBe(0);
    expect(epochPercent(Number.NaN)).toBe(0);
  });
});

describe("TONE_ANNOUNCEMENT", () => {
  it("is the same text for every height while the tone holds", () => {
    const a = healthSentence(network({ height: 100 })).tone;
    const b = healthSentence(network({ height: 101 })).tone;
    expect(TONE_ANNOUNCEMENT[a]).toBe(TONE_ANNOUNCEMENT[b]);
  });

  it("changes text when the network degrades", () => {
    const healthy = healthSentence(network()).tone;
    const degraded = healthSentence(network({ validatorsSigning: 4 })).tone;
    expect(TONE_ANNOUNCEMENT[healthy]).not.toBe(TONE_ANNOUNCEMENT[degraded]);
    expect(TONE_ANNOUNCEMENT[degraded]).toContain("degraded");
  });

  it("never mentions a block height", () => {
    for (const text of Object.values(TONE_ANNOUNCEMENT)) expect(text).not.toMatch(/\d/);
  });
});
