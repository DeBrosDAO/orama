import { describe, expect, it } from "vitest";
import { ALICE, BOB, decode, fieldsOf, said, valoper } from "./fixtures";

const HOUSES = "/orama.houses.v1.";
const FEES = "/orama.fees.v1.";
const EMISSION = "/orama.emission.v1.";
const SHIELDED = "/orama.shielded.v1.";

describe("x/houses decoders", () => {
  it("TestHouses_submit_proposal_names_the_kind_from_the_one_set_field", () => {
    const upgrade = decode(`${HOUSES}MsgSubmitProposal`, {
      proposer: ALICE,
      content: { software_upgrade: { name: "v2", height: "5000" }, parameter_change: null },
    });
    expect(said(upgrade)).toBe("Alice submitted a software upgrade proposal");
    expect(fieldsOf(upgrade)).toMatchObject({ Kind: "software upgrade", "Upgrade name": "v2", "Upgrade height": "5,000" });
    const spend = decode(`${HOUSES}MsgSubmitProposal`, {
      proposer: ALICE,
      content: { development_spend: { recipient: BOB, amount: "9000000000", epoch: "3" } },
    });
    expect(fieldsOf(spend)).toMatchObject({ Kind: "development spend", "Spend recipient": BOB, "Spend amount": "9000000000" });
    expect(spend.fields.find((f) => f.label === "Spend amount")?.kind).toBe("norama");
    const split = decode(`${HOUSES}MsgSubmitProposal`, {
      proposer: ALICE,
      content: { emission_split: { validator_percent: 55, storage_percent: 30, relay_percent: 10, development_percent: 5 } },
    });
    expect(fieldsOf(split)).toMatchObject({ "Validator share": "55%", "Development share": "5%" });
  });

  it("TestHouses_a_proposal_with_no_recognised_content_is_not_given_a_kind", () => {
    expect(said(decode(`${HOUSES}MsgSubmitProposal`, { proposer: ALICE }))).toBe("Alice submitted a governance proposal");
  });

  it("TestHouses_votes_say_how_the_voter_voted_and_in_what_capacity", () => {
    expect(said(decode(`${HOUSES}MsgVoteToken`, { voter: ALICE, proposal_id: "4", option: "YES" }))).toBe("Alice voted yes on proposal #4 as a token holder");
    expect(said(decode(`${HOUSES}MsgVoteOperator`, { voter: ALICE, proposal_id: "4", option: "ABSTAIN" }, ), true)).toBe(
      "Alice tried to vote abstain on proposal #4 as a node operator",
    );
  });

  it("TestHouses_bond_and_execute", () => {
    const lock = decode(`${HOUSES}MsgLockHouseBond`, { signer: ALICE, amount: "100000000000" });
    expect(said(lock)).toBe("Alice locked 100 ORAMA as a house bond");
    expect(lock.amount).toBe("100000000000");
    expect(said(decode(`${HOUSES}MsgUnlockHouseBond`, { signer: ALICE }))).toBe("Alice unlocked their house bond");
    expect(said(decode(`${HOUSES}MsgExecuteProposal`, { signer: ALICE, proposal_id: "4" }))).toBe("Alice executed proposal #4");
  });
});

describe("x/fees and x/emission decoders", () => {
  it("TestFees_withdraw_earnings_moves_nothing_the_message_does_not_say", () => {
    const m = decode(`${FEES}MsgWithdrawEarnings`, { signer: ALICE, amount: "4000000000" });
    expect(said(m)).toBe("Alice withdrew 4 ORAMA from their earnings to their own bank balance");
    expect(said(m, true)).toBe("Alice tried to withdraw 4 ORAMA from their earnings to their own bank balance");
    expect(m.amount).toBe("4000000000");
  });

  it("TestEmission_faucet_is_a_request_not_a_grant", () => {
    const m = decode(`${EMISSION}MsgFaucet`, { signer: ALICE, recipient: BOB, amount: "1000000000" });
    expect(said(m)).toBe("Alice requested 1 ORAMA from the faucet for Bob");
    expect(said(m, true)).toBe("Alice tried to request 1 ORAMA from the faucet for Bob");
  });
});

describe("x/shielded decoders", () => {
  it("TestShielded_transfer_shows_only_what_is_public", () => {
    const m = decode(`${SHIELDED}MsgShieldedTransfer`, { signer: ALICE, bundle: btoa("x".repeat(300)) });
    expect(m.actor).toBeNull();
    expect(said(m)).toBe("A shielded transfer was submitted");
    expect(said(m, true)).toBe("A shielded transfer was attempted");
    expect(fieldsOf(m)).toEqual({ "Bundle size": "300 bytes", "Public contents": "A proof bundle the explorer does not decode" });
    expect(m.amount).toBeNull();
    expect(JSON.stringify(m)).not.toContain(ALICE);
  });

  it("TestShielded_shield_and_unshield_name_the_signer_and_no_amount", () => {
    const shield = decode(`${SHIELDED}MsgShield`, { signer: ALICE, bundle: btoa("abc") });
    expect(said(shield)).toBe("Alice shielded funds from their bank balance");
    expect(shield.amount).toBeNull();
    expect(said(decode(`${SHIELDED}MsgShieldEarnings`, { signer: ALICE }))).toBe("Alice shielded funds from their earnings");
    const unshield = decode(`${SHIELDED}MsgUnshield`, { signer: ALICE, target: "UNSHIELD_TARGET_BOND", validator: valoper(7) });
    expect(said(unshield)).toBe("Alice unshielded funds out of the shielded pool to their staking delegation");
    expect(said(decode(`${SHIELDED}MsgUnshield`, { signer: ALICE, target: "UNSHIELD_TARGET_FEE_TOPUP" }))).toContain("to their earnings account");
  });
});
