import { describe, expect, it } from "vitest";
import { mapMessage } from "../tx";
import { ALICE, BOB, body, ctx, decode, fieldsOf, said, valoper } from "./fixtures";

const BANK = "/cosmos.bank.v1beta1.";
const STAKING = "/cosmos.staking.v1beta1.";
const DISTR = "/cosmos.distribution.v1beta1.";
const norama = (amount: string) => ({ denom: "norama", amount });

describe("bank decoders", () => {
  it("TestBank_send_of_norama_alone_is_the_transfer_message", () => {
    const m = mapMessage(body(`${BANK}MsgSend`, { from_address: ALICE, to_address: BOB, amount: [norama("5")] }), null, ctx);
    expect(m).toMatchObject({ type: "send", amount: "5" });
  });

  it("TestBank_send_of_a_foreign_coin_is_an_action_naming_it", () => {
    const m = decode(`${BANK}MsgSend`, { from_address: ALICE, to_address: BOB, amount: [{ denom: "factory/x/gold", amount: "5" }] });
    expect(said(m)).toBe("Alice sent 5 factory/x/gold to Bob");
    expect(m.category).toBe("transfers");
  });

  it("TestBank_multi_send_and_parameter_updates", () => {
    expect(said(decode(`${BANK}MsgMultiSend`, { inputs: [{}], outputs: [{}, {}] }))).toBe("Alice made a multi-send from 1 input to 2 outputs");
    const update = decode(`${BANK}MsgUpdateParams`, { authority: BOB });
    expect(said(update)).toBe("Bob updated the bank parameters");
    expect(said(update, true)).toBe("Bob tried to update the bank parameters");
  });
});

describe("staking decoders", () => {
  it("TestStaking_delegate_of_a_foreign_coin_names_the_coin", () => {
    const m = decode(`${STAKING}MsgDelegate`, { delegator_address: ALICE, validator_address: valoper(7), amount: { denom: "factory/x/gold", amount: "5" } });
    expect(said(m)).toBe("Alice delegated 5 factory/x/gold to val-one");
    expect(m.amount).toBeNull();
  });

  it("TestStaking_create_validator_and_edit_validator", () => {
    const create = decode(`${STAKING}MsgCreateValidator`, {
      delegator_address: ALICE,
      validator_address: valoper(1),
      description: { moniker: "atlas" },
      commission: { rate: "0.1", max_rate: "0.2", max_change_rate: "0.01" },
      value: norama("10000000000"),
      min_self_delegation: "1",
    });
    expect(said(create)).toBe("Alice created the validator “atlas” with a self-delegation of 10 ORAMA");
    expect(create.amount).toBe("10000000000");
    expect(fieldsOf(create)).toMatchObject({ Moniker: "atlas", "Commission rate": "0.1" });
    const edit = decode(`${STAKING}MsgEditValidator`, {
      validator_address: valoper(1),
      description: { moniker: "[do-not-modify]", website: "https://atlas.example" },
      commission_rate: "0.15",
    });
    expect(said(edit)).toBe("val-one edited its validator settings");
    expect(fieldsOf(edit)).toEqual({ Website: "https://atlas.example", "Commission rate": "0.15" });
  });

  it("TestStaking_redelegate_and_cancel_unbonding", () => {
    const re = decode(`${STAKING}MsgBeginRedelegate`, {
      delegator_address: ALICE,
      validator_src_address: valoper(7),
      validator_dst_address: valoper(8),
      amount: norama("3000000000"),
    });
    expect(said(re)).toBe("Alice moved 3 ORAMA of stake from val-one to val-one");
    expect(re.amount).toBe("3000000000");
    expect(said(decode(`${STAKING}MsgCancelUnbondingDelegation`, { delegator_address: ALICE, validator_address: valoper(7), amount: norama("1000000000"), creation_height: "88" }))).toBe(
      "Alice cancelled the unbonding of 1 ORAMA from val-one",
    );
  });
});

describe("distribution, slashing, feegrant, protocol decoders", () => {
  it("TestDistribution_withdraw_reward_states_no_amount", () => {
    const m = decode(`${DISTR}MsgWithdrawDelegatorReward`, { delegator_address: ALICE, validator_address: valoper(7) });
    expect(said(m)).toBe("Alice withdrew staking rewards from val-one");
    expect(m.amount).toBeNull();
  });

  it("TestDistribution_other_messages", () => {
    expect(said(decode(`${DISTR}MsgSetWithdrawAddress`, { delegator_address: ALICE, withdraw_address: BOB }))).toBe("Alice set their reward withdraw address to Bob");
    expect(said(decode(`${DISTR}MsgWithdrawValidatorCommission`, { validator_address: valoper(7) }))).toBe("val-one withdrew its validator commission");
    expect(said(decode(`${DISTR}MsgFundCommunityPool`, { depositor: ALICE, amount: [norama("2000000000")] }))).toBe("Alice funded the community pool with 2 ORAMA");
    expect(said(decode(`${DISTR}MsgDepositValidatorRewardsPool`, { depositor: ALICE, validator_address: valoper(7), amount: [norama("1000000000")] }))).toBe(
      "Alice deposited 1 ORAMA into the rewards pool of val-one",
    );
  });

  it("TestSlashing_unjail", () => {
    const m = decode("/cosmos.slashing.v1beta1.MsgUnjail", { validator_addr: valoper(7) });
    expect(said(m)).toBe("val-one unjailed itself");
    expect(m.actor?.address).toBeDefined();
  });

  it("TestFeegrant_grant_revoke_prune", () => {
    const grant = decode("/cosmos.feegrant.v1beta1.MsgGrantAllowance", {
      granter: ALICE,
      grantee: BOB,
      allowance: { "@type": "/cosmos.feegrant.v1beta1.BasicAllowance", spend_limit: [norama("500000000")], expiration: null },
    });
    expect(said(grant)).toBe("Alice granted Bob a fee allowance");
    expect(fieldsOf(grant)).toEqual({ Grantee: BOB, "Allowance type": "BasicAllowance", "Spend limit": "0.5 ORAMA" });
    expect(said(decode("/cosmos.feegrant.v1beta1.MsgRevokeAllowance", { granter: ALICE, grantee: BOB }))).toBe("Alice revoked the fee allowance of Bob");
    expect(said(decode("/cosmos.feegrant.v1beta1.MsgPruneAllowances", { pruner: ALICE }))).toBe("Alice pruned expired fee allowances");
  });

  it("TestProtocol_upgrade_evidence_and_parameter_messages", () => {
    const up = decode("/cosmos.upgrade.v1beta1.MsgSoftwareUpgrade", { authority: BOB, plan: { name: "v2", height: "9000", info: "https://x" } });
    expect(said(up)).toBe("Bob scheduled the software upgrade “v2” at height 9,000");
    expect(said(decode("/cosmos.upgrade.v1beta1.MsgCancelUpgrade", { authority: BOB }))).toBe("Bob cancelled the scheduled software upgrade");
    expect(said(decode("/cosmos.evidence.v1beta1.MsgSubmitEvidence", { submitter: ALICE, evidence: { "@type": "/cosmos.evidence.v1beta1.Equivocation" } }))).toBe(
      "Alice submitted evidence of validator misbehaviour",
    );
    expect(said(decode("/cosmos.consensus.v1.MsgUpdateParams", { authority: BOB }))).toBe("Bob updated the consensus parameters");
    expect(said(decode("/cosmos.auth.v1beta1.MsgUpdateParams", { authority: BOB }))).toBe("Bob updated the account parameters");
  });
});
