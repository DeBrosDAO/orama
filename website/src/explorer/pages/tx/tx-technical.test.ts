import { describe, expect, it } from "vitest";
import { messageTypeUrls } from "./tx-technical";

describe("messageTypeUrls", () => {
  it("TestMessageTypeUrls_reads_every_message_in_order", () => {
    const raw = JSON.stringify({
      body: { messages: [{ "@type": "/cosmos.bank.v1beta1.MsgSend" }, { "@type": "/orama.storage.v1.MsgCreateDeal" }] },
    });
    expect(messageTypeUrls(raw)).toEqual(["/cosmos.bank.v1beta1.MsgSend", "/orama.storage.v1.MsgCreateDeal"]);
  });

  it("TestMessageTypeUrls_not_json_gives_empty_list", () => {
    expect(messageTypeUrls("{oops")).toEqual([]);
    expect(messageTypeUrls("")).toEqual([]);
  });

  it("TestMessageTypeUrls_missing_body_or_messages_gives_empty_list", () => {
    expect(messageTypeUrls("null")).toEqual([]);
    expect(messageTypeUrls("[]")).toEqual([]);
    expect(messageTypeUrls(JSON.stringify({ body: {} }))).toEqual([]);
    expect(messageTypeUrls(JSON.stringify({ body: { messages: [] } }))).toEqual([]);
  });

  it("TestMessageTypeUrls_skips_messages_without_a_type", () => {
    const raw = JSON.stringify({ body: { messages: [{ nope: 1 }, "x", { "@type": 5 }, { "@type": "/a.B" }] } });
    expect(messageTypeUrls(raw)).toEqual(["/a.B"]);
  });
});
