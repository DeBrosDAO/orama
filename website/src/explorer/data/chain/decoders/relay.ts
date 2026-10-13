import { define, field, plural, sp, table } from "./kit";

const MODULE = "Relay";
const FINGERPRINT_BYTES = 20;

export const RELAY_DECODERS = table(
  define("/orama.relay.v1.MsgRegisterRelay", {
    module: MODULE,
    tag: "Register relay",
    actor: "operator",
    verb: ["registered", "register"],
    object: (r) => sp`${r.bool("exit") ? "an exit relay" : "a relay"} on node ${r.name("node_id")}`,
    fields: (r) => [
      field("Node ID", r.name("node_id")),
      field("Kind", r.bool("exit") ? "Exit relay" : "Relay"),
      field("RSA fingerprint", r.hex("rsa_fingerprint", FINGERPRINT_BYTES)),
    ],
  }),
  define("/orama.relay.v1.MsgUpdateRelay", {
    module: MODULE,
    tag: "Update relay",
    actor: "operator",
    verb: ["rotated", "rotate"],
    object: (r) => sp`the fingerprint of ${r.bool("exit") ? "the exit relay" : "the relay"} on node ${r.name("node_id")}`,
    fields: (r) => [
      field("Node ID", r.name("node_id")),
      field("Kind", r.bool("exit") ? "Exit relay" : "Relay"),
      field("RSA fingerprint", r.hex("rsa_fingerprint", FINGERPRINT_BYTES)),
    ],
  }),
  define("/orama.relay.v1.MsgReportEpoch", {
    module: MODULE,
    tag: "Report relay epoch",
    actor: "reporter",
    verb: ["reported", "report"],
    object: (r) => sp`${plural(r.count("entries"), "relay observation")} for epoch ${r.uint("epoch")}`,
    fields: (r) => [
      field("Epoch", r.uint("epoch")),
      field("Chunk index", r.uint("chunk_index")),
      field("Chunk count", r.uint("chunk_count")),
      field("Observations", String(r.count("entries"))),
      field("Inputs root", r.hex("inputs_root")),
    ],
  }),
  define("/orama.relay.v1.MsgUpdateReporters", {
    module: MODULE,
    tag: "Update relay reporters",
    actor: "signer",
    verb: ["set", "set"],
    object: (r) => sp`the relay reporter list to ${plural(r.count("reporters"), "account")}`,
    fields: (r) => [field("Reporters", r.list("reporters"))],
  }),
);
