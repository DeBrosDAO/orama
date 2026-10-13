import { describe, expect, it } from "vitest";
import { createClient } from "./client";
import { FakeGateway, addr } from "./fake-gateway";
import { proposalJson } from "./governance-fixtures";
import { loadGovernance, loadProposal, loadVote, readProposal } from "./governance";
import { queryPath } from "./module-query";

const nanos = (iso: string) => String(BigInt(Date.parse(iso)) * 1_000_000n);
const client = (gw: FakeGateway) => createClient(gw.fetch, "https://gw.example/v1/chain");
describe("proposals", () => {
  it("TestReadProposal_reads_the_lifecycle_the_tallies_and_a_parameter_change", () => {
    const p = readProposal(
      proposalJson(4, { parameter_change: { token_quorum: "334000000000000000", token_pass_threshold: "0.500000000000000000", voting_period_seconds: "1209600", house_bond: "5000000000", max_eligible_per_prefix16: 2, max_eligible_per_asn: 3 } }),
    );
    expect(p).toMatchObject({ id: 4, status: "voting", vetoEndsAt: null, timelockEndsAt: null, tokens: { yes: "700", no: "100", abstain: "0" }, operators: { yes: 3, no: 1, abstain: 0, eligible: 5 } });
    expect(p.submittedAt).toBe("2026-10-01T00:00:00.000Z");
    expect(p.content.kind).toBe("Parameter change");
    expect(p.content.facts.map((f) => [f.label, f.value])).toEqual([
      ["Token house quorum", "33.4%"],
      ["Token house pass threshold", "50.0%"],
      ["Voting period", "14 days"],
      ["House bond", "5000000000"],
      ["Eligible operators per /16", "2"],
      ["Eligible operators per ASN", "3"],
    ]);
  });

  it.each([
    ["software_upgrade", { name: "v2", height: "5000" }, "Software upgrade", ["Upgrade name", "At height"]],
    ["emission_split", { validator_percent: 60, storage_percent: 25, relay_percent: 10, development_percent: 5 }, "Emission split change", ["Validators", "Storage", "Relay", "Development"]],
    ["development_spend", { recipient: addr(2), amount: "1000", epoch: "7" }, "Development spend", ["Recipient", "Amount", "Emission epoch claimed"]],
    ["power_bounds", { m_max: "250000000000000000", activate_m: true }, "Power bounds change", ["M bound", "Activates M"]],
    ["relay_reporters", { add: [addr(3)], remove: [] }, "Relay reporter change", ["Add"]],
    ["allow_list", { code_upload_add: [addr(3)], code_upload_remove: [], adapter_add: [], adapter_remove: [] }, "Allow-list change", ["Code upload: add"]],
  ])("TestReadProposal_%s_content_has_its_own_facts", (key, content, kind, labels) => {
    const p = readProposal(proposalJson(1, { [key]: content }));
    expect(p.content.kind).toBe(kind);
    expect(p.content.facts.map((f) => f.label)).toEqual(labels);
  });

  it("TestReadProposal_a_passed_proposal_carries_its_veto_and_timelock_ends", () => {
    const p = readProposal(
      proposalJson(1, { software_upgrade: { name: "v2", height: "5" } }, { status: "TIMELOCK", veto_end_unix_nano: nanos("2026-10-16T00:00:00Z"), timelock_end_unix_nano: nanos("2026-11-16T00:00:00Z") }),
    );
    expect(p.status).toBe("timelock");
    expect(p.vetoEndsAt).toBe("2026-10-16T00:00:00.000Z");
    expect(p.timelockEndsAt).toBe("2026-11-16T00:00:00.000Z");
  });

  it("TestReadProposal_a_failure_reason_is_cleaned_and_a_content_with_no_kind_is_rejected", () => {
    const failed = readProposal(proposalJson(1, { software_upgrade: { name: "v2", height: "5" } }, { status: "FAILED", fail_reason: "boom\u0000" }));
    expect(failed.failReason).toBe("boom");
    expect(() => readProposal(proposalJson(1, {}))).toThrow("The chain sent a malformed proposal content.");
    expect(() => readProposal(proposalJson(1, { software_upgrade: { name: "v", height: "1" } }, { status: "SOMETHING" }))).toThrow("proposal status");
  });

  it("TestLoadProposal_a_missing_proposal_is_null_and_another_id_is_rejected", async () => {
    const gw = new FakeGateway().on(queryPath("orama.houses.v1.Query/Proposal", { proposal_id: 2 }), { proposal: proposalJson(3, { software_upgrade: { name: "v", height: "1" } }) });
    expect(await loadProposal(client(gw), 9)).toBeNull();
    await expect(loadProposal(client(gw), 2)).rejects.toThrow("another id than asked for");
  });
});

describe("votes and rules", () => {
  it("TestLoadVote_reads_the_option_and_a_missing_vote_is_null", async () => {
    const gw = new FakeGateway().on(queryPath("orama.houses.v1.Query/Vote", { proposal_id: 2, voter: addr(1) }), { vote: { proposal_id: "2", voter: addr(1), option: "ABSTAIN" } });
    expect(await loadVote(client(gw), 2, addr(1))).toEqual({ proposalId: 2, voter: addr(1), option: "abstain" });
    expect(await loadVote(client(gw), 2, addr(2))).toBeNull();
  });

  it("TestLoadGovernance_reads_the_parameters_and_what_enacted_proposals_set", async () => {
    const gw = new FakeGateway()
      .on(queryPath("orama.houses.v1.Query/Params"), {
        params: {
          bootstrap_exit_stake: "271000000000000",
          token_quorum: "334000000000000000",
          token_pass_threshold: "500000000000000000",
          voting_period_seconds: "1209600",
          house_bond: "5000000000",
          max_eligible_per_prefix16: 2,
          max_eligible_per_asn: 3,
          min_house_size: 5,
          veto_window_seconds: "604800",
          parameter_timelock_seconds: "1209600",
          upgrade_timelock_seconds: "5184000",
          spend_timelock_seconds: "604800",
        },
      })
      .on(queryPath("orama.houses.v1.Query/Enacted"), {
        enacted: { scheduled_upgrade: { name: "", height: "0" }, emission_split: null, m_activated: false, m_max: "0", relay_reporters: [addr(2)], code_upload_allow: [], adapter_allow: [] },
      });
    const gov = await loadGovernance(client(gw));
    expect(gov.params).toMatchObject({ tokenQuorum: 0.334, tokenPassThreshold: 0.5, votingPeriodSeconds: 1209600, minHouseSize: 5, vetoWindowSeconds: 604800 });
    expect(gov.enacted).toEqual({ scheduledUpgrade: null, emissionSplit: null, powerBoundsActive: false, relayReporters: [addr(2)], codeUploadAllow: [], adapterAllow: [] });
  });
});
