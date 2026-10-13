import { describe, expect, it } from "vitest";
import { ctx } from "./fixtures";
import { DECODERS, decoderFor } from "./index";
import type { Env } from "./kit";
import { PROTO_ROOT, parseMsgService, readChainTxProtos, readRegistered, undecoded } from "./test-support";

const protos = readChainTxProtos();
const registered = readRegistered();
const protoUrls = protos.map((p) => p.typeUrl);
const env: Env = { ...ctx, signer: null };

/**
 * The explorer's coverage gate, in the spirit of the repo's e2e coverage gate:
 * every message the chain defines must have a decoder, so a message added to a
 * proto or to the app cannot reach the explorer as "unknown". When this fails,
 * add a decoder in data/chain/decoders/<module>.ts and register it in index.ts.
 */
describe("every Msg the chain defines has a decoder", () => {
  it("TestCompleteness_every_tx_proto_rpc_has_a_decoder", () => {
    expect(protoUrls.length).toBeGreaterThanOrEqual(60);
    expect(undecoded(protoUrls, DECODERS)).toEqual([]);
  });

  it("TestCompleteness_every_message_the_chain_app_registers_has_a_decoder", () => {
    expect(registered.msgs.length).toBeGreaterThanOrEqual(100);
    expect(undecoded(registered.msgs, DECODERS)).toEqual([]);
  });

  it("TestCompleteness_no_decoder_is_registered_for_a_type_the_chain_does_not_have", () => {
    const known = new Set([...protoUrls, ...registered.msgs]);
    expect(Object.keys(DECODERS).filter((url) => !known.has(url)).sort()).toEqual([]);
  });

  it("TestCompleteness_the_proto_services_and_the_app_registry_agree_on_the_orama_messages", () => {
    const fromRegistry = registered.msgs.filter((url) => url.startsWith("/orama.")).sort();
    expect([...protoUrls].sort()).toEqual(fromRegistry);
  });

  it("TestCompleteness_every_module_with_a_tx_proto_is_covered", () => {
    const modules = new Set(protoUrls.map((url) => url.split(".")[1]));
    expect([...modules].sort()).toEqual(["archive", "cnft", "emission", "fees", "houses", "market", "nodes", "relay", "shielded", "storage", "token"]);
  });

  it("TestCompleteness_the_standard_messages_the_chain_wires_are_decoded", () => {
    for (const url of [
      "/cosmos.bank.v1beta1.MsgSend",
      "/cosmos.staking.v1beta1.MsgCreateValidator",
      "/cosmos.distribution.v1beta1.MsgWithdrawDelegatorReward",
      "/cosmos.slashing.v1beta1.MsgUnjail",
      "/cosmos.feegrant.v1beta1.MsgGrantAllowance",
      "/cosmwasm.wasm.v1.MsgExecuteContract",
    ]) {
      expect(decoderFor(url), url).toBeDefined();
    }
  });
});

describe("the gate itself", () => {
  const FIXTURE = `
    syntax = "proto3";
    package orama.fixture.v1;
    // Msg is a fixture module's service.
    service Msg {
      option (cosmos.msg.v1.service) = true;
      rpc Known(MsgKnown) returns (MsgKnownResponse);
      rpc BrandNew(MsgBrandNew) returns (MsgBrandNewResponse);
    }
    message MsgKnown { option (cosmos.msg.v1.signer) = "signer"; string signer = 1; }
    message MsgKnownResponse {}
    message MsgBrandNew {
      string signer = 1 [(cosmos_proto.scalar) = "cosmos.AddressString"];
      repeated string things = 2;
      Inner inner = 3 [(gogoproto.nullable) = false];
      message Inner { string x = 1; }
    }
    message MsgBrandNewResponse {}
  `;

  it("TestCompleteness_a_msg_added_to_a_proto_without_a_decoder_fails_the_gate", () => {
    const urls = parseMsgService(FIXTURE).map((p) => p.typeUrl);
    expect(urls).toEqual(["/orama.fixture.v1.MsgKnown", "/orama.fixture.v1.MsgBrandNew"]);
    const decoders = { "/orama.fixture.v1.MsgKnown": () => null };
    expect(undecoded(urls, decoders)).toEqual(["/orama.fixture.v1.MsgBrandNew"]);
  });

  it("TestCompleteness_the_gate_passes_once_the_decoder_exists", () => {
    const urls = parseMsgService(FIXTURE).map((p) => p.typeUrl);
    const decoders = { "/orama.fixture.v1.MsgKnown": () => null, "/orama.fixture.v1.MsgBrandNew": () => null };
    expect(undecoded(urls, decoders)).toEqual([]);
  });

  it("TestCompleteness_the_parser_reads_request_fields_and_skips_nested_messages_and_options", () => {
    const brandNew = parseMsgService(FIXTURE).find((p) => p.typeUrl.endsWith("MsgBrandNew"));
    expect(brandNew?.fields).toEqual(["signer", "things", "inner"]);
  });

  it("TestCompleteness_a_proto_without_a_msg_service_has_no_messages", () => {
    expect(parseMsgService('syntax = "proto3"; package orama.q.v1; service Query { rpc Get(QueryGet) returns (QueryGetResponse); }')).toEqual([]);
  });

  it("TestCompleteness_the_parser_reads_the_real_protos_to_the_fields_the_chain_encodes", () => {
    expect(PROTO_ROOT).toContain("chain/proto/orama");
    for (const proto of protos) {
      const shape = registered.shapes[proto.typeUrl];
      expect(shape, proto.typeUrl).toBeDefined();
      expect(Object.keys(shape ?? {}).filter((k) => k !== "@type").sort(), proto.typeUrl).toEqual([...proto.fields].sort());
    }
  });
});

describe("every decoder reads the fields the chain writes", () => {
  it("TestCompleteness_each_decoder_decodes_the_chains_own_zero_message_reading_only_fields_that_exist", () => {
    for (const url of registered.msgs) {
      const shape = registered.shapes[url] ?? {};
      const read = new Set<string>();
      const watched = new Proxy(shape, {
        get(target, key, receiver) {
          if (typeof key === "string") read.add(key);
          return Reflect.get(target, key, receiver);
        },
      });
      const decoded = decoderFor(url)?.(watched, env);
      expect(decoded, `${url} decodes an all-zero message`).not.toBeNull();
      expect(decoded?.type, url).toBe("action");
      const unknownFields = [...read].filter((key) => !(key in shape));
      expect(unknownFields, `${url} reads fields the chain does not write`).toEqual([]);
    }
  });
});
