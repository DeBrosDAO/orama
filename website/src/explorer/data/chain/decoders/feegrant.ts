import { define, field, sp, table } from "./kit";
import type { Reader } from "./kit";

const MODULE = "Fee grants";
const TYPE_PREFIX = "/cosmos.feegrant.v1beta1.";
const MAX_TYPE = 80;

/** The allowance's type without its package: "BasicAllowance". */
const allowanceType = (r: Reader) => r.at("allowance").text("@type", MAX_TYPE).replace(TYPE_PREFIX, "").replace(/^\//, "");

export const FEEGRANT_DECODERS = table(
  define("/cosmos.feegrant.v1beta1.MsgGrantAllowance", {
    module: MODULE,
    tag: "Grant fee allowance",
    actor: "granter",
    verb: ["granted", "grant"],
    object: (r) => sp`${r.party("grantee")} a fee allowance`,
    fields: (r) => [
      field("Grantee", r.text("grantee"), "address"),
      field("Allowance type", allowanceType(r)),
      field("Spend limit", r.at("allowance").coinsText("spend_limit")),
      field("Expiration", r.at("allowance").text("expiration")),
    ],
  }),
  define("/cosmos.feegrant.v1beta1.MsgRevokeAllowance", {
    module: MODULE,
    tag: "Revoke fee allowance",
    actor: "granter",
    verb: ["revoked", "revoke"],
    object: (r) => sp`the fee allowance of ${r.party("grantee")}`,
    fields: (r) => [field("Grantee", r.text("grantee"), "address")],
  }),
  define("/cosmos.feegrant.v1beta1.MsgPruneAllowances", {
    module: MODULE,
    tag: "Prune fee allowances",
    actor: "pruner",
    verb: ["pruned", "prune"],
    object: () => ["expired fee allowances"],
  }),
);
