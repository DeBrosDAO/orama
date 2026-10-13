import { describe, expect, it } from "vitest";
import { describeMessage } from "../../../model/describe";
import type { TxMessage } from "../../../model/types";
import { mapMessage } from "../tx";
import { ALICE, BOB, body, ctx, decode, fieldsOf, said } from "./fixtures";
import { bytesText, bpsText, digitsOf, grouped } from "./format";
import { MAX_LISTED, MAX_TEXT } from "./kit";

const NODE = "/orama.nodes.v1.";
const BIDI = "‮";

describe("chain-written text is sanitised and bounded", () => {
  it("TestKit_control_and_bidi_characters_are_dropped_from_sentence_and_fields", () => {
    const m = decode(`${NODE}MsgClaimNodeName`, { operator: ALICE, node_id: `n${BIDI}1\u0007\u0000`, name: `at${BIDI}las\u001b[31m` });
    expect(said(m)).toBe("Alice claimed the name “atlas[31m” for node n1");
    expect(JSON.stringify(m)).not.toMatch(/[\u0000-\u001f‮]/);
  });

  it("TestKit_a_very_long_text_is_cut_in_the_sentence_and_in_the_fields", () => {
    const huge = "x".repeat(100_000);
    const m = decode(`${NODE}MsgRegisterCluster`, { operator: ALICE, cluster_id: huge, base_domain: huge, metadata_uri: huge });
    expect(said(m).length).toBeLessThan(MAX_TEXT + 60);
    expect(said(m).endsWith("…")).toBe(true);
    for (const value of Object.values(fieldsOf(m))) expect(value.length).toBeLessThanOrEqual(MAX_TEXT);
  });

  it("TestKit_html_stays_inert_text_parts", () => {
    const html = "<script>alert(1)</script><b onclick=1>x</b>";
    const m = decode(`${NODE}MsgRegisterNode`, { operator: ALICE, node_id: html, region_hint: html });
    const kinds = new Set(describeMessage(m).map((p) => p.kind));
    expect([...kinds].sort()).toEqual(["text", "wallet"]);
    expect(said(m)).toContain(html);
  });

  it("TestKit_a_long_list_is_counted_not_dumped", () => {
    const endpoints = Array.from({ length: 5000 }, (_, i) => `e${i}.example`);
    const m = decode(`${NODE}MsgRegisterNode`, { operator: ALICE, node_id: "n1", endpoints });
    const listed = fieldsOf(m).Endpoints ?? "";
    expect(listed.split(", ").length).toBe(MAX_LISTED + 1);
    expect(listed).toContain("+4994 more");
  });

  it("TestKit_a_field_of_the_wrong_type_is_ignored_not_fatal", () => {
    const m = decode(`${NODE}MsgRegisterNode`, { operator: ALICE, node_id: 12, roles: "storage", endpoints: { a: 1 }, hot_key: [1], asn: { x: 1 } });
    expect(said(m)).toBe("Alice registered node (empty)");
  });

  it("TestKit_an_address_field_that_is_not_an_address_is_shown_as_cleaned_text", () => {
    const m = decode("/orama.token.v1.MsgMint", { sender: ALICE, denom: "norama", recipient: `<b>${BIDI}notanaddress`, amount: "1" });
    expect(said(m)).toBe("Alice minted 0.000000001 ORAMA to <b>notanaddress");
    expect(m.done.some((p) => p.kind === "wallet" && p.ref.address.includes("notanaddress"))).toBe(false);
  });

  it("TestKit_an_actor_that_is_not_an_address_does_not_become_a_wallet_link", () => {
    const m = decode(`${NODE}MsgRetireNode`, { operator: "<script>", node_id: "n1" });
    expect(m.actor).toBeNull();
    expect(m.done[0]).toEqual({ kind: "text", text: "<script> retired node n1" });
  });

  it("TestKit_an_empty_actor_falls_back_to_the_transaction_signer", () => {
    expect(said(decode(`${NODE}MsgRetireNode`, { operator: "", node_id: "n1" }))).toBe("Alice retired node n1");
    const noSigner = mapMessage(body(`${NODE}MsgRetireNode`, { node_id: "n1" }), null, ctx);
    expect(said(noSigner)).toBe("An unnamed account retired node n1");
  });

  it("TestKit_a_denom_with_markup_is_cleaned_and_cut", () => {
    const m = decode("/orama.token.v1.MsgBurn", { sender: ALICE, denom: `${"d".repeat(500)}${BIDI}<i>`, amount: "5" });
    expect(said(m).length).toBeLessThan(200);
  });
});

describe("failed messages and unknown types", () => {
  it("TestKit_a_failed_message_reads_tried_to_with_the_same_object", () => {
    const m = decode(`${NODE}MsgClaimNodeName`, { operator: ALICE, node_id: "n1", name: "atlas" });
    expect(said(m, true)).toBe("Alice tried to claim the name “atlas” for node n1");
    expect(said(m, false)).toBe("Alice claimed the name “atlas” for node n1");
  });

  it("TestKit_an_unregistered_type_stays_unknown", () => {
    for (const typeUrl of ["/thirdparty.contract.v1.MsgSwap", "/orama.nodes.v1.MsgNotAThing", "/cosmos.authz.v1beta1.MsgGrant", "/ibc.core.client.v1.MsgCreateClient"]) {
      const m: TxMessage = mapMessage({ "@type": typeUrl, sender: ALICE }, { address: ALICE }, ctx);
      expect(m).toEqual({ type: "unknown", typeUrl, signer: { address: ALICE } });
    }
  });

  it("TestKit_a_body_without_a_type_is_a_malformed_read", () => {
    expect(() => mapMessage({ sender: ALICE }, null, ctx)).toThrow(/malformed/);
  });
});

describe("number and size formatting", () => {
  it("TestFormat_digits_accepts_only_integers", () => {
    expect(digitsOf("12")).toBe("12");
    expect(digitsOf(12)).toBe("12");
    expect(digitsOf("-5")).toBeNull();
    expect(digitsOf("-5", true)).toBe("-5");
    for (const bad of ["1e9", "01", "1.5", "", " 1", null, undefined, {}, "9".repeat(41), 1.5, Number.MAX_SAFE_INTEGER + 2]) expect(digitsOf(bad)).toBeNull();
  });

  it("TestFormat_bytes_are_truncated_never_rounded_up", () => {
    expect(bytesText("0")).toBe("0 bytes");
    expect(bytesText("1")).toBe("1 byte");
    expect(bytesText("1023")).toBe("1,023 bytes");
    expect(bytesText("1024")).toBe("1 KiB (1,024 bytes)");
    expect(bytesText("1048575")).toBe("1023.99 KiB (1,048,575 bytes)");
    expect(bytesText("1125899906842624000")).toContain("PiB");
  });

  it("TestFormat_basis_points_and_grouping", () => {
    expect(bpsText("0")).toBe("0%");
    expect(bpsText("250")).toBe("2.5%");
    expect(bpsText("10000")).toBe("100%");
    expect(bpsText("1")).toBe("0.01%");
    expect(grouped("1234567890123456789012")).toBe("1,234,567,890,123,456,789,012");
  });
});

describe("amounts use the ORAMA formatting", () => {
  it("TestKit_a_norama_amount_is_an_amount_part_so_the_ui_formats_it", () => {
    const m = decode(`${NODE}MsgBondNode`, { operator: BOB, node_id: "n1", role: "ROLE_RELAY", amount: "1234567890" });
    expect(m.done.find((p) => p.kind === "amount")).toEqual({ kind: "amount", norama: "1234567890" });
    expect(said(m)).toContain("1.23456789 ORAMA");
  });

  it("TestKit_an_amount_that_is_not_an_integer_is_unreadable_and_not_an_amount", () => {
    const m = decode(`${NODE}MsgBondNode`, { operator: ALICE, node_id: "n1", role: "ROLE_RELAY", amount: "1e9" });
    expect(m.amount).toBeNull();
    expect(m.done.some((p) => p.kind === "amount")).toBe(false);
    expect(said(m)).toContain("(unreadable)");
  });
});
