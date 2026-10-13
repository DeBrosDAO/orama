import { define, field, paramsUpdate, sp, table } from "./kit";

const MAX_TYPE = 80;

/** Messages of the protocol's own modules (upgrade, evidence, consensus, auth). Most need a module authority to succeed. */
export const PROTOCOL_DECODERS = table(
  define("/cosmos.upgrade.v1beta1.MsgSoftwareUpgrade", {
    module: "Upgrade",
    tag: "Software upgrade",
    actor: "authority",
    verb: ["scheduled", "schedule"],
    object: (r) => sp`the software upgrade “${r.at("plan").name("name")}” at height ${r.at("plan").int("height")}`,
    fields: (r) => [
      field("Upgrade name", r.at("plan").text("name")),
      field("Height", r.at("plan").int("height")),
      field("Info", r.at("plan").text("info")),
    ],
  }),
  define("/cosmos.upgrade.v1beta1.MsgCancelUpgrade", {
    module: "Upgrade",
    tag: "Cancel upgrade",
    actor: "authority",
    verb: ["cancelled", "cancel"],
    object: () => ["the scheduled software upgrade"],
  }),
  define("/cosmos.evidence.v1beta1.MsgSubmitEvidence", {
    module: "Evidence",
    tag: "Submit evidence",
    actor: "submitter",
    verb: ["submitted", "submit"],
    object: () => ["evidence of validator misbehaviour"],
    fields: (r) => [field("Evidence type", r.at("evidence").text("@type", MAX_TYPE).replace(/^\//, ""))],
  }),
  paramsUpdate("/cosmos.consensus.v1.MsgUpdateParams", "Consensus", "consensus parameters"),
  paramsUpdate("/cosmos.auth.v1beta1.MsgUpdateParams", "Auth", "account parameters"),
);
