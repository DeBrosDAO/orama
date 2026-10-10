import { describe, expect, it } from "vitest";
import { amountOf, categoryOf, describeMessage } from "./describe";
import type { TxMessage } from "./types";

const A = { address: "orama1a" };
const B = { address: "orama1b", label: "Bob" };
const V = { moniker: "val-3", operator: "orama1v" };

const send: TxMessage = { type: "send", from: A, to: B, amount: "12500000000" };

describe("describeMessage", () => {
  it("TestDescribeMessage_send", () => {
    const parts = describeMessage(send);
    expect(parts.map((p) => p.kind)).toEqual(["wallet", "text", "amount", "text", "wallet"]);
    expect(parts[1]).toEqual({ kind: "text", text: " sent " });
  });

  it("TestDescribeMessage_failed_uses_the_tried_wording", () => {
    expect(describeMessage(send, true)[1]).toEqual({ kind: "text", text: " tried to send " });
  });

  it("TestDescribeMessage_every_type_starts_with_the_actor", () => {
    const messages: TxMessage[] = [
      send,
      { type: "delegate", delegator: A, validator: V, amount: "1" },
      { type: "undelegate", delegator: A, validator: V, amount: "1" },
      { type: "claim_rewards", delegator: A, validator: V, amount: "1" },
      { type: "storage_deal", owner: A, provider: B, amount: "1", replicas: 1, visibility: "private" },
      { type: "unknown", typeUrl: "/x.y.MsgZ", signer: A },
    ];
    for (const m of messages) {
      const first = describeMessage(m)[0];
      expect(first).toEqual({ kind: "wallet", ref: A });
    }
  });

  it("TestDescribeMessage_unknown_still_says_something", () => {
    const parts = describeMessage({ type: "unknown", typeUrl: "/x.y.MsgZ", signer: A });
    expect(parts.some((p) => p.kind === "text" && p.text.includes("x.y.MsgZ"))).toBe(true);
  });

  it("TestDescribeMessage_storage_pluralises_copies", () => {
    const one = describeMessage({ type: "storage_deal", owner: A, provider: B, amount: "1", replicas: 1, visibility: "public" });
    const three = describeMessage({ type: "storage_deal", owner: A, provider: B, amount: "1", replicas: 3, visibility: "public" });
    expect(JSON.stringify(one)).toContain("1 copy");
    expect(JSON.stringify(three)).toContain("3 copies");
  });
});

describe("categoryOf and amountOf", () => {
  it("TestCategoryOf_groups_messages", () => {
    expect(categoryOf(send)).toBe("transfers");
    expect(categoryOf({ type: "claim_rewards", delegator: A, validator: V, amount: "1" })).toBe("staking");
    expect(categoryOf({ type: "unknown", typeUrl: "/x", signer: A })).toBe("other");
  });

  it("TestAmountOf_null_when_the_message_has_none", () => {
    expect(amountOf(send)).toBe("12500000000");
    expect(amountOf({ type: "unknown", typeUrl: "/x", signer: A })).toBeNull();
  });
});
