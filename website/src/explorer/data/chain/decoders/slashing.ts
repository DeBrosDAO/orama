import { define, paramsUpdate, table } from "./kit";

const MODULE = "Slashing";

export const SLASHING_DECODERS = table(
  define("/cosmos.slashing.v1beta1.MsgUnjail", {
    module: MODULE,
    tag: "Unjail",
    category: "staking",
    actor: "validator_addr",
    verb: ["unjailed", "unjail"],
    object: () => ["itself"],
  }),
  paramsUpdate("/cosmos.slashing.v1beta1.MsgUpdateParams", MODULE, "slashing parameters"),
);
