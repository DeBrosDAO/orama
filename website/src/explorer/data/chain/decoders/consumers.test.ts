import { describe, expect, it } from "vitest";
import { actionInvolves, amountOf, categoryOf, tagOf } from "../../../model/describe";
import { activityLabel, partsToText } from "../../../pages/wallet/activity-model";
import { sendItem } from "../../../pages/wallet/fixtures";
import { flowOf } from "../../../pages/tx/tx-parties";
import { messageTypesText } from "../../../pages/tx/tx-messages";
import { ALICE, BOB, CAROL, decode } from "./fixtures";

const NODE = "/orama.nodes.v1.";

describe("an action message in the explorer's shared helpers", () => {
  it("TestConsumers_tag_category_and_amount", () => {
    const bond = decode(`${NODE}MsgBondNode`, { operator: ALICE, node_id: "n1", role: "ROLE_RELAY", amount: "5" });
    expect(tagOf(bond)).toBe("Bond node");
    expect(categoryOf(bond)).toBe("staking");
    expect(amountOf(bond)).toBe("5");
    const retire = decode(`${NODE}MsgRetireNode`, { operator: ALICE, node_id: "n1" });
    expect(categoryOf(retire)).toBe("other");
    expect(amountOf(retire)).toBeNull();
    expect(messageTypesText([bond, retire, bond])).toBe("Bond node, Retire node");
  });

  it("TestConsumers_the_flow_names_the_signer_and_no_recipient", () => {
    const f = flowOf(decode(`${NODE}MsgRetireNode`, { operator: ALICE, node_id: "n1" }));
    expect(f.from).toEqual({ role: "Signer", wallet: { address: ALICE, label: "Alice" } });
    expect(f.to).toBeNull();
  });

  it("TestConsumers_a_wallet_is_involved_when_it_acts_or_is_named_in_the_sentence", () => {
    const m = decode("/orama.token.v1.MsgMint", { sender: ALICE, denom: "norama", recipient: BOB, amount: "1" });
    expect(actionInvolves(m, ALICE)).toBe(true);
    expect(actionInvolves(m, BOB)).toBe(true);
    expect(actionInvolves(m, CAROL)).toBe(false);
  });

  it("TestConsumers_wallet_activity_labels_an_action_by_its_tag_and_says_when_it_failed", () => {
    const message = decode(`${NODE}MsgClaimNodeName`, { operator: ALICE, node_id: "n1", name: "atlas" });
    expect(partsToText(activityLabel(sendItem({ message })))).toBe("Claim node name");
    expect(partsToText(activityLabel(sendItem({ message, status: { ok: false, reason: "name taken" } })))).toBe("Failed: claim node name · name taken");
  });
});
