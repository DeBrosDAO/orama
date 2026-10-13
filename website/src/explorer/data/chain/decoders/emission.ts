import { define, field, sp, table } from "./kit";

export const EMISSION_DECODERS = table(
  define("/orama.emission.v1.MsgFaucet", {
    module: "Emission",
    tag: "Faucet",
    actor: "signer",
    verb: ["requested", "request"],
    object: (r) => sp`${r.amountPart("amount")} from the faucet for ${r.party("recipient")}`,
    amount: (r) => r.norama("amount"),
    fields: (r) => [field("Recipient", r.text("recipient"), "address"), field("Amount", r.norama("amount") ?? "", "norama")],
  }),
);
