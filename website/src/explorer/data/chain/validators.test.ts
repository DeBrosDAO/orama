import { describe, expect, it } from "vitest";
import { consensusAddress } from "./directory";
import { addr, chainFixture, delegationPath, keyOf, powerPath, stakingValidator, valoper } from "./fake-gateway";
import type { FakeGateway } from "./fake-gateway";
import { createChainSource } from "./source";
import { MAX_OPERATOR_LOOKUPS } from "./validator-detail";
import { decShare, legacyDec } from "./wire";

async function setup() {
  const gw = await chainFixture();
  return { gw, source: createChainSource({ fetcher: gw.fetch }) };
}

const ALPHA = addr(7);
const BETA = addr(8);

/** What the indexer's validator epoch row looks like (chain/indexer/agg_model.go), a Go struct as JSON. */
const epochRow = (epoch: number, over: Record<string, unknown> = {}) => ({
  epoch,
  operator: valoper(7),
  consensus_address: "oramavalcons1x",
  comet_power: 600_000_000,
  signed_blocks: 95,
  missed_blocks: 5,
  uptime_bps: 9500,
  slashes: 0,
  slashed_burned: "0",
  jailed: false,
  status: "bonded",
  ...over,
});

function indexValidator(gw: FakeGateway, n: number, rows: { epochs?: unknown[]; slashes?: unknown[]; jails?: unknown[] }) {
  const base = `index/validators/${valoper(n)}`;
  gw.on(`${base}/epochs?limit=60`, { operator: valoper(n), page: 1, limit: 60, epochs: rows.epochs ?? [] });
  gw.on(`${base}/slashes?limit=50`, { operator: valoper(n), page: 1, limit: 50, slashes: rows.slashes ?? [] });
  gw.on(`${base}/jails?limit=20`, { operator: valoper(n), page: 1, limit: 20, jails: rows.jails ?? [] });
}

describe("legacyDec", () => {
  it("TestLegacyDec_reads_both_spellings_of_a_decimal", () => {
    expect(legacyDec("0.050000000000000000", "x")).toBe(0.05);
    expect(legacyDec("50000000000000000", "x")).toBe(0.05);
    expect(legacyDec("1000000000000000000", "x")).toBe(1);
    expect(legacyDec("0", "x")).toBe(0);
    expect(legacyDec("1", "x")).toBeCloseTo(1e-18, 30);
  });

  it("TestLegacyDec_the_lambda_the_live_stagenet_gateway_sent", () => {
    // 10,000 of 271,000 ORAMA bonded: lambda = 0.036900369...
    expect(legacyDec("36900369003690037", "lambda")).toBeCloseTo(10 / 271, 12);
    expect(decShare("36900369003690037", "lambda")).toBeLessThan(0.04);
  });

  it.each(["", "abc", "-1", "1e3", "0.5.1", " 1"])("TestLegacyDec_refuses_%j", (text) => {
    expect(() => legacyDec(text, "x")).toThrow(/malformed/);
  });

  it("TestDecShare_a_share_above_one_is_malformed", () => {
    expect(() => decShare("2000000000000000000", "x")).toThrow(/malformed/);
    expect(() => legacyDec(5, "x")).toThrow(/malformed/);
  });
});

describe("getValidators", () => {
  it("TestGetValidators_carries_status_commission_and_staked_amount_from_the_staking_record", async () => {
    const { source } = await setup();
    const alpha = (await source.getValidators()).validators.find((v) => v.ref.moniker === "Alpha");
    expect(alpha).toMatchObject({ status: "bonded", commission: 0.1, tokens: "4000000000000" });
  });

  it("TestGetValidators_handover_is_made_of_the_figures_x_power_exposes", async () => {
    const { source } = await setup();
    const { handover } = await source.getValidators();
    expect(handover).toEqual({
      lambda: 0.25,
      stake: { bonded: "7000000000000", exit: "271000000000000" },
      epochs: { current: 12, deadline: 365 },
      preGateCap: 0.95,
      cap: { current: 0.05, normal: 0.05, reduced: 0.03, stepDownCount: 60 },
    });
  });

  it("TestGetValidators_reads_lambda_as_the_gateway_writes_it", async () => {
    const { gw, source } = await setup();
    gw.on("query/orama.power.v1.Query/Lambda", { lambda: "36900369003690037", last_updated_epoch: "7", current_cap_bps: "300" });
    const set = await source.getValidators();
    expect(set.lambda).toBeCloseTo(0.0369, 4);
    expect(set.handover.cap.current).toBe(0.03);
  });

  it("TestGetValidators_a_malformed_staking_status_rejects_the_read", async () => {
    const { gw, source } = await setup();
    gw.on("staking/validators", { validators: [stakingValidator(7, 11, { status: "BOND_STATUS_WEIRD" })] });
    await expect(source.getValidators()).rejects.toThrow("The chain sent a malformed validator status.");
  });

  it("TestGetValidators_a_power_module_failure_is_a_readable_error", async () => {
    const { gw, source } = await setup();
    gw.failing.add("query/orama.power.v1.Query/Params");
    await expect(source.getValidators()).rejects.toThrow("The chain could not be read (HTTP 500).");
  });
});

describe("getValidator", () => {
  function wire(gw: FakeGateway) {
    gw.on(delegationPath(ALPHA, valoper(7)), { delegation_response: { balance: { denom: "norama", amount: "1500000000000" } } });
    gw.on(powerPath(valoper(7)), { operator_address: valoper(7), comet_power: "600000000", operator: addr(1) });
  }

  it("TestGetValidator_splits_the_staked_amount_into_self_bond_and_delegated_by_others", async () => {
    const { gw, source } = await setup();
    wire(gw);
    const d = await source.getValidator(ALPHA);
    expect(d).toMatchObject({
      validator: { ref: { moniker: "Alpha", operator: ALPHA }, type: "committee", status: "bonded", jailed: false },
      selfBond: "1500000000000",
      delegatedByOthers: "2500000000000",
      commission: { rate: 0.1, maxRate: 0.2, maxChangePerDay: 0.01, updatedAt: null },
      minSelfDelegation: "1",
      unbondsAt: null,
      power: { cometPower: "600000000", operator: { kind: "registered", address: addr(1) } },
    });
  });

  it("TestGetValidator_a_validator_that_bonded_nothing_to_itself_has_a_zero_self_bond", async () => {
    const { gw, source } = await setup();
    gw.on(powerPath(valoper(7)), { operator_address: valoper(7), comet_power: "600000000", operator: "unlinked" });
    const d = await source.getValidator(ALPHA);
    expect(d?.selfBond).toBe("0");
    expect(d?.delegatedByOthers).toBe("4000000000000");
    expect(d?.power?.operator).toEqual({ kind: "unlinked" });
  });

  it("TestGetValidator_a_self_bond_above_the_total_never_gives_a_negative_delegated_amount", async () => {
    const { gw, source } = await setup();
    wire(gw);
    gw.on(delegationPath(ALPHA, valoper(7)), { delegation_response: { balance: { denom: "norama", amount: "9000000000000" } } });
    expect((await source.getValidator(ALPHA))?.delegatedByOthers).toBe("0");
  });

  it("TestGetValidator_x_power_not_knowing_the_validator_leaves_power_out", async () => {
    const { gw, source } = await setup();
    gw.on(powerPath(valoper(7)), { operator_address: valoper(7), comet_power: "0", operator: "" });
    expect((await source.getValidator(ALPHA))?.power).toBeNull();
  });

  it("TestGetValidator_no_validator_with_that_operator_is_null_not_an_invented_record", async () => {
    const { source } = await setup();
    expect(await source.getValidator(addr(99))).toBeNull();
  });

  it("TestGetValidator_a_failing_read_rejects_without_the_nodes_words", async () => {
    const { gw, source } = await setup();
    wire(gw);
    gw.failing.add(powerPath(valoper(7)));
    await expect(source.getValidator(ALPHA)).rejects.toThrow("The chain could not be read (HTTP 500).");
    gw.failing.clear();
    gw.failing.add(delegationPath(ALPHA, valoper(7)));
    await expect(source.getValidator(ALPHA)).rejects.not.toThrow(/corrupted/);
  });

  it("TestGetValidator_text_the_validator_wrote_is_cleaned_and_cut", async () => {
    const { gw, source } = await setup();
    wire(gw);
    gw.on("staking/validators", {
      validators: [
        stakingValidator(7, 11, {
          description: { moniker: "Alpha", website: "https://a.example‮.org", details: `${"x".repeat(400)}\u0007` },
        }),
      ],
    });
    const d = await source.getValidator(ALPHA);
    expect(d?.website).toBe("https://a.example.org");
    expect(d?.details.length).toBeLessThanOrEqual(280);
    expect(d?.details).not.toContain("\u0007");
  });

  it("TestGetValidator_an_unbonding_validator_says_when_it_finishes", async () => {
    const { gw, source } = await setup();
    wire(gw);
    gw.on("staking/validators", {
      validators: [stakingValidator(7, 11, { status: "BOND_STATUS_UNBONDING", jailed: true, unbonding_time: "2026-10-20T00:00:00Z", description: { moniker: "Alpha" } })],
    });
    const d = await source.getValidator(ALPHA);
    expect(d?.unbondsAt).toBe("2026-10-20T00:00:00.000Z");
    expect(d?.validator.status).toBe("unbonding");
  });

  it("TestGetValidator_a_changed_commission_carries_the_time_of_the_change", async () => {
    const { gw, source } = await setup();
    wire(gw);
    gw.on("staking/validators", {
      validators: [
        stakingValidator(7, 11, {
          description: { moniker: "Alpha" },
          commission: { commission_rates: { rate: "0.050000000000000000", max_rate: "1.000000000000000000", max_change_rate: "0.010000000000000000" }, update_time: "2026-10-01T00:00:00Z" },
        }),
      ],
    });
    expect((await source.getValidator(ALPHA))?.commission).toEqual({ rate: 0.05, maxRate: 1, maxChangePerDay: 0.01, updatedAt: "2026-10-01T00:00:00.000Z" });
  });
});

describe("getValidatorHistory", () => {
  it("TestGetValidatorHistory_reads_epochs_slashes_and_jail_periods_newest_first", async () => {
    const { gw, source } = await setup();
    indexValidator(gw, 7, {
      epochs: [epochRow(12), epochRow(11, { missed_blocks: 0, signed_blocks: 100, uptime_bps: 10000, comet_power: 500_000_000 })],
      slashes: [{ height: 90, time: "2026-10-08T11:00:00.123456789Z", epoch: 12, reason: "missing_signature", power: 3, burned: "1000" }],
      jails: [
        { jailed_height: 90, jailed_time: "2026-10-08T11:00:00Z", reason: "missing_signature", unjailed_height: 0 },
        { jailed_height: 0, unjailed_height: 40, unjailed_time: "2026-10-01T00:00:00Z" },
      ],
    });
    const h = await source.getValidatorHistory(ALPHA);
    expect(h?.epochs.map((e) => e.epoch)).toEqual([12, 11]);
    expect(h?.epochs[0]).toMatchObject({ cometPower: 600_000_000, signedBlocks: 95, missedBlocks: 5, uptime: 0.95, status: "bonded", jailed: false });
    expect(h?.epochs[1]?.uptime).toBe(1);
    expect(h?.slashes).toEqual([{ height: 90, time: "2026-10-08T11:00:00.123Z", epoch: 12, reason: "missing_signature", power: 3, burned: "1000" }]);
    expect(h?.jails[0]).toMatchObject({ jailedHeight: 90, unjailedHeight: null, unjailedTime: null, reason: "missing_signature" });
    expect(h?.jails[1]).toMatchObject({ jailedHeight: null, jailedTime: null, unjailedHeight: 40, reason: "" });
  });

  it("TestGetValidatorHistory_an_epoch_with_no_commit_has_no_uptime_rather_than_zero", async () => {
    const { gw, source } = await setup();
    indexValidator(gw, 7, { epochs: [epochRow(12, { signed_blocks: 0, missed_blocks: 0, uptime_bps: 0 })] });
    expect((await source.getValidatorHistory(ALPHA))?.epochs[0]?.uptime).toBeNull();
  });

  it("TestGetValidatorHistory_a_validator_with_no_slash_or_jail_has_empty_lists", async () => {
    const { gw, source } = await setup();
    gw.on(`index/validators/${valoper(7)}/epochs?limit=60`, { epochs: [epochRow(12)] });
    gw.on(`index/validators/${valoper(7)}/slashes?limit=50`, { operator: valoper(7) });
    gw.on(`index/validators/${valoper(7)}/jails?limit=20`, { operator: valoper(7) });
    const h = await source.getValidatorHistory(ALPHA);
    expect(h?.slashes).toEqual([]);
    expect(h?.jails).toEqual([]);
  });

  it("TestGetValidatorHistory_a_validator_the_index_has_not_seen_is_null", async () => {
    const { source } = await setup();
    expect(await source.getValidatorHistory(ALPHA)).toBeNull();
  });

  it("TestGetValidatorHistory_a_node_without_an_indexer_rejects_with_a_readable_error", async () => {
    const { gw, source } = await setup();
    gw.failing.add(`index/validators/${valoper(7)}/epochs?limit=60`);
    await expect(source.getValidatorHistory(ALPHA)).rejects.toThrow("The chain could not be read (HTTP 500).");
  });

  it("TestGetValidatorHistory_an_unknown_operator_is_null_without_asking_the_index", async () => {
    const { gw, source } = await setup();
    expect(await source.getValidatorHistory(addr(99))).toBeNull();
    expect(gw.asked.some((p) => p.startsWith("index/validators/"))).toBe(false);
  });

  it("TestGetValidatorHistory_a_malformed_row_rejects_naming_the_field_not_the_value", async () => {
    const { gw, source } = await setup();
    indexValidator(gw, 7, { epochs: [epochRow(12, { status: "<script>" })] });
    await expect(source.getValidatorHistory(ALPHA)).rejects.toThrow("The chain sent a malformed validator status.");
  });
});

describe("getValidatorSigning", () => {
  const summary = (n: number, last: unknown) => ({ operator: valoper(n), moniker: `V${n}`, status: "bonded", jailed: false, tokens: "1", comet_power: 1, updated_epoch: 12, last_epoch: last });

  it("TestGetValidatorSigning_gives_each_validators_newest_epoch_by_its_operator_wallet", async () => {
    const { gw, source } = await setup();
    gw.on("index/validators?page=1&limit=100", { page: 1, limit: 100, validators: [summary(7, epochRow(12)), summary(8, epochRow(12, { slashes: 2, missed_blocks: 40, signed_blocks: 60, uptime_bps: 6000 }))] });
    expect(await source.getValidatorSigning()).toEqual([
      { operator: ALPHA, epoch: 12, uptime: 0.95, missedBlocks: 5, slashes: 0 },
      { operator: BETA, epoch: 12, uptime: 0.6, missedBlocks: 40, slashes: 2 },
    ]);
  });

  it("TestGetValidatorSigning_a_validator_with_no_closed_epoch_is_left_out", async () => {
    const { gw, source } = await setup();
    gw.on("index/validators?page=1&limit=100", { validators: [summary(7, undefined)] });
    expect(await source.getValidatorSigning()).toEqual([]);
  });

  it("TestGetValidatorSigning_before_any_epoch_closes_the_list_is_empty", async () => {
    const { gw, source } = await setup();
    gw.on("index/validators?page=1&limit=100", { page: 1, limit: 100 });
    expect(await source.getValidatorSigning()).toEqual([]);
  });

  it("TestGetValidatorSigning_follows_a_full_page_to_the_next", async () => {
    const { gw, source } = await setup();
    const full = Array.from({ length: 100 }, () => summary(7, epochRow(12)));
    gw.on("index/validators?page=1&limit=100", { validators: full });
    gw.on("index/validators?page=2&limit=100", { validators: [summary(8, epochRow(12))] });
    expect(await source.getValidatorSigning()).toHaveLength(101);
  });

  it("TestGetValidatorSigning_a_node_without_an_indexer_rejects", async () => {
    const { gw, source } = await setup();
    gw.failing.add("index/validators?page=1&limit=100");
    await expect(source.getValidatorSigning()).rejects.toThrow("The chain could not be read (HTTP 500).");
  });
});

describe("getOperatorGroups", () => {
  it("TestGetOperatorGroups_adds_up_the_validators_one_operator_runs", async () => {
    const { gw, source } = await setup();
    gw.on(powerPath(valoper(7)), { comet_power: "60", operator: addr(1) });
    gw.on(powerPath(valoper(8)), { comet_power: "40", operator: addr(1) });
    const { groups, unresolved } = await source.getOperatorGroups();
    expect(unresolved).toBe(0);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.operator).toEqual({ kind: "registered", address: addr(1) });
    expect(groups[0]?.validators.map((v) => v.ref.moniker).sort()).toEqual(["Alpha", "Beta"]);
    expect(groups[0]?.power).toBeCloseTo(1);
  });

  it("TestGetOperatorGroups_validators_without_a_registered_node_share_one_unlinked_group", async () => {
    const { gw, source } = await setup();
    gw.on(powerPath(valoper(7)), { comet_power: "60", operator: addr(1) });
    gw.on(powerPath(valoper(8)), { comet_power: "40", operator: "unlinked" });
    const { groups } = await source.getOperatorGroups();
    expect(groups.map((g) => (g.operator.kind === "unlinked" ? "unlinked" : g.operator.address))).toEqual([addr(1), "unlinked"]);
    expect(groups.map((g) => g.power)).toEqual([0.6, 0.4]);
  });

  it("TestGetOperatorGroups_a_validator_outside_the_active_set_has_no_power_to_group", async () => {
    const { gw, source } = await setup();
    gw.on("validators?page=1&per_page=100", { jsonrpc: "2.0", id: -1, result: { validators: [], total: "0" } });
    expect((await source.getOperatorGroups()).groups).toEqual([]);
    expect(gw.asked.some((p) => p.includes("ValidatorPower"))).toBe(false);
  });

  /** A chain with `count` active validators, each with its own consensus key. */
  async function manyValidators(count: number) {
    const { gw, source } = await setup();
    const numbers = Array.from({ length: count }, (_, i) => i + 20);
    const consensus = await Promise.all(numbers.map((n) => consensusAddress(keyOf(n))));
    gw.on("staking/validators", { validators: numbers.map((n) => stakingValidator(n, n, { description: { moniker: `V${n}` } })) });
    gw.on("validators?page=1&per_page=100", {
      jsonrpc: "2.0",
      id: -1,
      result: { validators: consensus.map((address, i) => ({ address, voting_power: String(1000 - i) })), total: String(count) },
    });
    for (const n of numbers) gw.on(powerPath(valoper(n)), { comet_power: "1", operator: addr(n) });
    return { gw, source };
  }

  it("TestGetOperatorGroups_one_load_with_a_large_validator_set_stays_inside_the_gateways_query_burst", async () => {
    // The gateway gives a client network a burst of 30 module queries; a validators page makes four of its own.
    const gatewayBurst = 30;
    const otherQueries = 4;
    const { gw, source } = await manyValidators(150);
    const before = gw.asked.length;
    const { groups, unresolved } = await source.getOperatorGroups();
    const lookups = gw.asked.slice(before).filter((p) => p.includes("ValidatorPower"));
    expect(lookups).toHaveLength(MAX_OPERATOR_LOOKUPS);
    expect(MAX_OPERATOR_LOOKUPS + otherQueries).toBeLessThanOrEqual(gatewayBurst);
    expect(unresolved).toBe(150 - MAX_OPERATOR_LOOKUPS);
    expect(groups.reduce((n, g) => n + g.validators.length, 0)).toBe(MAX_OPERATOR_LOOKUPS);
  });

  it("TestGetOperatorGroups_the_validators_page_and_a_validators_page_share_one_set_of_lookups", async () => {
    const { gw, source } = await manyValidators(40);
    await source.getOperatorGroups();
    const after = gw.asked.length;
    await source.getOperatorGroups();
    expect(gw.asked.length).toBe(after);
  });

  it("TestGetOperatorGroups_a_failed_read_is_not_remembered", async () => {
    const { gw, source } = await setup();
    gw.on(powerPath(valoper(7)), { comet_power: "60", operator: addr(1) });
    gw.failing.add(powerPath(valoper(8)));
    await expect(source.getOperatorGroups()).rejects.toThrow("HTTP 500");
    gw.failing.clear();
    gw.on(powerPath(valoper(8)), { comet_power: "40", operator: addr(1) });
    expect((await source.getOperatorGroups()).groups).toHaveLength(1);
  });

  it("TestGetOperatorGroups_a_failing_lookup_rejects_instead_of_a_partial_grouping", async () => {
    const { gw, source } = await setup();
    gw.on(powerPath(valoper(7)), { comet_power: "60", operator: addr(1) });
    gw.failing.add(powerPath(valoper(8)));
    await expect(source.getOperatorGroups()).rejects.toThrow("The chain could not be read (HTTP 500).");
  });

  it("TestGetOperatorGroups_an_operator_that_is_not_an_orama_address_is_malformed", async () => {
    const { gw, source } = await setup();
    gw.on(powerPath(valoper(7)), { comet_power: "60", operator: "not-an-address" });
    gw.on(powerPath(valoper(8)), { comet_power: "40", operator: addr(1) });
    await expect(source.getOperatorGroups()).rejects.toThrow("The chain sent a malformed validator operator.");
  });
});
