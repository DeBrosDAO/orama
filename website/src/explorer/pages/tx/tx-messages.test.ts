import { describe, expect, it } from "vitest";
import type { TxMessage } from "../../model/types";
import { extraActionCount, firstMessage, messageTypesText, moreActionsText } from "./tx-messages";

const alice = { address: "orama1alice" };
const bob = { address: "orama1bob" };
const validator = { moniker: "val-1", operator: "orama1val" };
const send: TxMessage = { type: "send", from: alice, to: bob, amount: "1" };
const delegate: TxMessage = { type: "delegate", delegator: alice, validator, amount: "2" };

describe("firstMessage", () => {
  it("TestFirstMessage_is_null_for_no_messages", () => {
    expect(firstMessage([])).toBeNull();
  });

  it("TestFirstMessage_is_the_first_of_many", () => {
    expect(firstMessage([send, delegate])).toBe(send);
  });
});

describe("extraActionCount", () => {
  it("TestExtraActionCount_zero_one_and_many", () => {
    expect(extraActionCount([])).toBe(0);
    expect(extraActionCount([send])).toBe(0);
    expect(extraActionCount([send, delegate, send])).toBe(2);
  });
});

describe("moreActionsText", () => {
  it("TestMoreActionsText_empty_when_the_headline_covers_everything", () => {
    expect(moreActionsText([])).toBe("");
    expect(moreActionsText([send])).toBe("");
  });

  it("TestMoreActionsText_singular_and_plural", () => {
    expect(moreActionsText([send, delegate])).toBe("+1 more action");
    expect(moreActionsText([send, delegate, send, send])).toBe("+3 more actions");
  });
});

describe("messageTypesText", () => {
  it("TestMessageTypesText_empty_for_no_messages", () => {
    expect(messageTypesText([])).toBe("");
  });

  it("TestMessageTypesText_names_each_type_once_in_order", () => {
    expect(messageTypesText([send, delegate, send])).toBe("Transfer, Stake");
    expect(messageTypesText([send])).toBe("Transfer");
  });
});
