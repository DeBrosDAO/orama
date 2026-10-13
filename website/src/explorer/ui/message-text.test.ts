import { describe, expect, it } from "vitest";
import type { SentencePart } from "../model/describe";
import { messageText, moreActionsNote, moreMessagesBadge, txLinkName } from "./message-text";

const ADDRESS = "orama1abcdefghijklmnopqrstuvwxyz0123456789ab";
const HASH = "0123456789ABCDEF".repeat(4);

const PARTS: SentencePart[] = [
  { kind: "wallet", ref: { address: ADDRESS, label: "Alice" } },
  { kind: "text", text: " sent " },
  { kind: "amount", norama: "12500000000" },
  { kind: "text", text: " to " },
  { kind: "wallet", ref: { address: ADDRESS } },
];

describe("messageText", () => {
  it("flattens a sentence into plain words", () => {
    expect(messageText(PARTS)).toMatch(/^Alice sent 12\.5 ORAMA to orama1abcd/);
  });

  it("uses a validator's moniker", () => {
    expect(messageText([{ kind: "validator", ref: { operator: ADDRESS, moniker: "val-3" } }])).toBe("val-3");
  });

  it("is empty for no parts", () => {
    expect(messageText([])).toBe("");
  });
});

describe("txLinkName", () => {
  it("appends the short hash", () => {
    const name = txLinkName(PARTS, HASH);
    expect(name).toContain(" · 012345…CDEF");
    expect(name.startsWith("Alice sent")).toBe(true);
  });
});

describe("moreMessagesBadge", () => {
  it("counts only the messages beyond the one described", () => {
    expect(moreMessagesBadge(3)).toBe("+2 more");
    expect(moreMessagesBadge(2)).toBe("+1 more");
  });

  it("is null for one or no message", () => {
    expect(moreMessagesBadge(1)).toBeNull();
    expect(moreMessagesBadge(0)).toBeNull();
  });
});

describe("moreActionsNote", () => {
  it("pluralises", () => {
    expect(moreActionsNote(2)).toContain("+1 more action ");
    expect(moreActionsNote(4)).toContain("+3 more actions ");
  });

  it("is null for a single message", () => {
    expect(moreActionsNote(1)).toBeNull();
  });
});
