import type { MessageField } from "../../../model/types";
import { define, field, parts, reader, table, text } from "./kit";
import type { Decoder, Reader } from "./kit";

const MODULE = "Shielded";
const SHIELDED = { module: MODULE, actor: "signer" } as const;

const TARGETS = [
  ["UNSPECIFIED", "an unspecified target"],
  ["BOND", "their staking delegation"],
  ["NODE_BOND", "their node role bond"],
  ["FEE_TOPUP", "their earnings account"],
  ["DEPOSIT", "their deposits and escrow"],
  ["CONTRACT", "a contract call"],
] as const;

const bundle = (r: Reader) => field("Bundle size", r.blobSize("bundle"));

/**
 * A shielded transfer has no signer and nobody to name. All the message
 * carries in public is a proof bundle, so that is all the row says.
 */
const shieldedTransfer: Decoder = (m, env) => {
  const r = reader(m, env);
  return {
    type: "action",
    typeUrl: "/orama.shielded.v1.MsgShieldedTransfer",
    module: MODULE,
    tag: "Shielded transfer",
    category: "other",
    actor: null,
    done: parts([text("A shielded transfer was submitted")]),
    tried: parts([text("A shielded transfer was attempted")]),
    amount: null,
    fields: [bundle(r), field("Public contents", "A proof bundle the explorer does not decode")].filter((f): f is MessageField => f !== null),
  };
};

export const SHIELDED_DECODERS = table(
  ["/orama.shielded.v1.MsgShieldedTransfer", shieldedTransfer],
  define("/orama.shielded.v1.MsgShield", {
    ...SHIELDED,
    tag: "Shield",
    verb: ["shielded", "shield"],
    object: () => ["funds from their bank balance"],
    fields: (r) => [bundle(r)],
  }),
  define("/orama.shielded.v1.MsgShieldEarnings", {
    ...SHIELDED,
    tag: "Shield earnings",
    verb: ["shielded", "shield"],
    object: () => ["funds from their earnings"],
    fields: (r) => [bundle(r)],
  }),
  define("/orama.shielded.v1.MsgUnshield", {
    ...SHIELDED,
    tag: "Unshield",
    verb: ["unshielded", "unshield"],
    object: (r) => ["funds out of the shielded pool to ", r.enumName("target", "UNSHIELD_TARGET_", TARGETS) || TARGETS[0][1]],
    fields: (r) => [
      field("Target", r.enumName("target", "UNSHIELD_TARGET_", TARGETS)),
      field("Validator", r.text("validator"), "address"),
      field("Node ID", r.text("node_id")),
      bundle(r),
    ],
  }),
);
