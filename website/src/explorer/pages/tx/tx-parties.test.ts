import { describe, expect, it } from "vitest";
import type { TxMessage } from "../../model/types";
import { flowOf, systemPartyLabel, validatorAsWallet, walletTitle } from "./tx-parties";

const alice = { address: "orama1alicealicealicealice" };
const bob = { address: "orama1bob", label: "Bob" };
const validator = { moniker: "Blockhaus", operator: "orama1validatoroperator00" };

describe("flowOf", () => {
  it("TestFlowOf_send_goes_from_sender_to_receiver", () => {
    const m: TxMessage = { type: "send", from: alice, to: bob, amount: "1" };
    const f = flowOf(m);
    expect(f.from).toEqual({ role: "From", wallet: alice });
    expect(f.to).toEqual({ role: "To", wallet: bob });
  });

  it("TestFlowOf_delegate_goes_to_the_validator_as_a_wallet", () => {
    const f = flowOf({ type: "delegate", delegator: alice, validator, amount: "1" });
    expect(f.from?.wallet).toBe(alice);
    expect(f.to?.wallet).toEqual({ address: validator.operator, label: "Blockhaus operator" });
  });

  it("TestFlowOf_claim_and_undelegate_come_from_the_validator", () => {
    for (const type of ["claim_rewards", "undelegate"] as const) {
      const f = flowOf({ type, delegator: alice, validator, amount: "1" });
      expect(f.from?.wallet.address).toBe(validator.operator);
      expect(f.to?.wallet).toBe(alice);
    }
  });

  it("TestFlowOf_storage_deal_goes_from_owner_to_provider", () => {
    const f = flowOf({ type: "storage_deal", owner: alice, provider: bob, amount: "1", replicas: 3, visibility: "private" });
    expect(f.from?.wallet).toBe(alice);
    expect(f.to?.wallet).toBe(bob);
  });

  it("TestFlowOf_storage_deal_has_no_provider_until_one_is_assigned", () => {
    const f = flowOf({ type: "storage_deal", owner: alice, provider: null, amount: "1", replicas: 3, visibility: "public" });
    expect(f.from?.wallet).toBe(alice);
    expect(f.to).toBeNull();
  });

  it("TestFlowOf_unknown_message_is_a_single_actor", () => {
    const f = flowOf({ type: "unknown", typeUrl: "/x.Msg", signer: alice });
    expect(f.from?.role).toBe("Signer");
    expect(f.to).toBeNull();
  });

  it("TestFlowOf_unknown_message_without_a_signer_has_no_party", () => {
    expect(flowOf({ type: "unknown", typeUrl: "/x.Msg", signer: null })).toEqual({ from: null, to: null });
  });
});

describe("validatorAsWallet", () => {
  it("TestValidatorAsWallet_uses_operator_address_and_moniker", () => {
    expect(validatorAsWallet(validator)).toEqual({ address: validator.operator, label: "Blockhaus operator" });
  });
});

describe("walletTitle", () => {
  it("TestWalletTitle_prefers_the_label", () => {
    expect(walletTitle(bob)).toBe("Bob");
  });

  it("TestWalletTitle_falls_back_to_a_shortened_address", () => {
    expect(walletTitle(alice)).toBe("Wallet orama1alic…lice");
  });

  it("TestWalletTitle_keeps_a_short_address_whole", () => {
    expect(walletTitle({ address: "orama1x" })).toBe("Wallet orama1x");
  });
});

describe("systemPartyLabel", () => {
  it("TestSystemPartyLabel_names_every_pool", () => {
    expect(systemPartyLabel("burned")).toBe("Network (burned) 🔥");
    expect(systemPartyLabel("minted")).toBe("Newly issued rewards");
    expect(systemPartyLabel("staked")).toBe("Staked");
    expect(systemPartyLabel("unbonding")).toBe("Unbonding");
    expect(systemPartyLabel("storage_escrow")).toBe("Storage escrow");
  });
});
