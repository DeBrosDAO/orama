import { describe, expect, it } from "vitest";
import { ChainReadError, withQuery } from "./client";
import { FakeGateway, addr } from "./fake-gateway";
import { RELAYS_METHOD, RELAY_PARAMS, page, pagePath, relayJson } from "./fake-network";
import { createChainSource } from "./source";
import { readRelay } from "./tor";

const PARAMS_PATH = "query/orama.relay.v1.Query/Params";
const REPORTERS_PATH = "query/orama.relay.v1.Query/Reporters";
const CURRENT_EPOCH = "query/orama.emission.v1.Query/CurrentEpoch";
const epochResultPath = (epoch: number) => withQuery("query/orama.relay.v1.Query/EpochResult", { json: JSON.stringify({ epoch: String(epoch) }) });
const source = (gw: FakeGateway) => createChainSource({ fetcher: gw.fetch });

function torGateway(relays: unknown[], epoch = 12): FakeGateway {
  return new FakeGateway()
    .on(pagePath(RELAYS_METHOD), page("relays", relays))
    .on(PARAMS_PATH, RELAY_PARAMS)
    .on(REPORTERS_PATH, { reporters: [addr(30), addr(31)] })
    .on(CURRENT_EPOCH, { epoch_state: { current_epoch: String(epoch), epoch_start_unix_nano: "0" } });
}

describe("readRelay", () => {
  it("TestReadRelay_turns_the_fingerprint_into_forty_hex_digits", () => {
    const relay = readRelay(relayJson(171, { exit: true }));
    expect(relay.fingerprint).toBe("AB".repeat(20));
    expect(relay).toMatchObject({ nodeId: "node-171", exit: true, jailed: false, prefix16: "10.171.0.0/16" });
  });

  it.each([
    ["a fingerprint of the wrong length", { rsa_fingerprint: btoa("abc") }],
    ["a fingerprint that is not base64", { rsa_fingerprint: "***" }],
    ["a node id with a slash", { node_id: "a/b" }],
    ["an operator that is not an account", { operator: "nope" }],
    ["a missing exit flag", { exit: undefined }],
  ])("TestReadRelay_rejects_%s", (_, over) => {
    expect(() => readRelay(relayJson(1, over))).toThrow(ChainReadError);
  });
});

describe("getTorRelays", () => {
  it("TestGetTorRelays_reads_relays_parameters_reporters_and_the_last_settlement", async () => {
    const gw = torGateway([relayJson(1), relayJson(2, { exit: true, jailed: true })])
      .on(epochResultPath(10), { result: { epoch: "10", quorum_met: true, activating: false, ceiling: "1000", minted: "400" } })
      .on(withQuery("query/orama.relay.v1.Query/RelayPayouts", { json: JSON.stringify({ epoch: "10", pagination: { limit: 100 } }) }), page("payouts", []));
    const tor = await source(gw).getTorRelays();
    expect(tor.relays.map((r) => [r.nodeId, r.exit, r.jailed])).toEqual([
      ["node-1", false, false],
      ["node-2", true, true],
    ]);
    expect(tor.truncated).toBe(false);
    expect(tor.params).toMatchObject({ reportersQuorum: 2, perRelayCap: "1000000000" });
    expect(tor.params.minUptime).toBeCloseTo(0.8);
    expect(tor.params.exitMultiplier).toBe(2);
    expect(tor.reporters).toEqual([addr(30), addr(31)]);
    expect(tor.lastSettlement).toEqual({ epoch: 10, quorumMet: true, activating: false, ceiling: "1000", minted: "400" });
  });

  it("TestGetTorRelays_reads_what_the_settled_epoch_paid_each_relay", async () => {
    const payoutsPath = withQuery("query/orama.relay.v1.Query/RelayPayouts", { json: JSON.stringify({ epoch: "10", pagination: { limit: 100 } }) });
    const gw = torGateway([relayJson(171), relayJson(2)])
      .on(epochResultPath(10), { result: { epoch: "10", quorum_met: true, activating: false, ceiling: "1000", minted: "400" } })
      .on(payoutsPath, page("payouts", [{ epoch: "10", rsa_fingerprint: relayJson(171).rsa_fingerprint, operator: addr(2), amount: "400" }]));
    const tor = await source(gw).getTorRelays();
    expect([...tor.payouts]).toEqual([["AB".repeat(20), "400"]]);
  });

  it("TestGetTorRelays_an_epoch_that_minted_nothing_asks_for_no_payouts", async () => {
    const gw = torGateway([relayJson(1)]).on(epochResultPath(10), {
      result: { epoch: "10", quorum_met: true, activating: true, ceiling: "1000", minted: "0" },
    });
    const tor = await source(gw).getTorRelays();
    expect(tor.payouts.size).toBe(0);
    expect(gw.asked.some((p) => p.includes("RelayPayouts"))).toBe(false);
  });

  it("TestGetTorRelays_a_malformed_payout_rejects", async () => {
    const payoutsPath = withQuery("query/orama.relay.v1.Query/RelayPayouts", { json: JSON.stringify({ epoch: "10", pagination: { limit: 100 } }) });
    const gw = torGateway([relayJson(1)])
      .on(epochResultPath(10), { result: { epoch: "10", quorum_met: true, activating: false, ceiling: "1000", minted: "400" } })
      .on(payoutsPath, page("payouts", [{ epoch: "10", rsa_fingerprint: "***", amount: "400" }]));
    await expect(source(gw).getTorRelays()).rejects.toThrow(ChainReadError);
  });

  it("TestGetTorRelays_an_epoch_with_no_settlement_is_null_with_the_epoch_named", async () => {
    const tor = await source(torGateway([relayJson(1)])).getTorRelays();
    expect(tor.lastSettlement).toBeNull();
    expect(tor.settlementEpoch).toBe(10);
  });

  it("TestGetTorRelays_an_epoch_still_inside_its_report_window_is_not_looked_up", async () => {
    const gw = torGateway([], 2);
    const tor = await source(gw).getTorRelays();
    expect(tor.lastSettlement).toBeNull();
    expect(tor.settlementEpoch).toBe(0);
    expect(gw.asked.some((p) => p.includes("EpochResult"))).toBe(false);
  });

  it("TestGetTorRelays_no_relays_is_an_empty_list_not_an_error", async () => {
    const tor = await source(torGateway([])).getTorRelays();
    expect(tor.relays).toEqual([]);
  });

  it("TestGetTorRelays_a_failing_settlement_read_rejects_rather_than_calling_it_unsettled", async () => {
    const gw = torGateway([relayJson(1)]);
    gw.failing.add(epochResultPath(10));
    await expect(source(gw).getTorRelays()).rejects.toThrow(ChainReadError);
  });

  it("TestGetTorRelays_a_failing_parameters_read_rejects", async () => {
    const gw = torGateway([relayJson(1)]);
    gw.failing.add(PARAMS_PATH);
    await expect(source(gw).getTorRelays()).rejects.toThrow("HTTP 500");
  });
});
