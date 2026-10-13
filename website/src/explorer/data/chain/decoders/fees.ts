import { define, field, sp, table } from "./kit";

export const FEES_DECODERS = table(
  define("/orama.fees.v1.MsgWithdrawEarnings", {
    module: "Fees",
    tag: "Withdraw earnings",
    category: "transfers",
    actor: "signer",
    verb: ["withdrew", "withdraw"],
    object: (r) => sp`${r.amountPart("amount")} from their earnings to their own bank balance`,
    amount: (r) => r.norama("amount"),
    fields: (r) => [field("Amount", r.norama("amount") ?? "", "norama")],
  }),
);
