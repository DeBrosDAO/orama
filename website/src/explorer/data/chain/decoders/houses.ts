import { define, field, sp, table } from "./kit";
import type { Reader } from "./kit";

const MODULE = "Houses";
const UNKNOWN_KIND = "governance";

const VOTE_OPTIONS = [
  ["VOTE_OPTION_UNSPECIFIED", "unspecified"],
  ["YES", "yes"],
  ["NO", "no"],
  ["ABSTAIN", "abstain"],
] as const;

/** The one governable action a proposal carries, by the field of ProposalContent that is set. */
const KINDS = [
  ["parameter_change", "parameter change"],
  ["software_upgrade", "software upgrade"],
  ["emission_split", "emission split change"],
  ["development_spend", "development spend"],
  ["power_bounds", "power bounds change"],
  ["relay_reporters", "relay reporter change"],
  ["allow_list", "allow-list change"],
] as const;

const proposal = (r: Reader) => `proposal #${r.uint("proposal_id")}`;
const vote = (r: Reader) => r.enumName("option", "", VOTE_OPTIONS) || "unspecified";

function kindOf(r: Reader): string {
  const content = r.at("content");
  return KINDS.find(([key]) => content.has(key))?.[1] ?? UNKNOWN_KIND;
}

function contentFields(r: Reader) {
  const content = r.at("content");
  const upgrade = content.at("software_upgrade");
  const spend = content.at("development_spend");
  const split = content.at("emission_split");
  return [
    field("Kind", kindOf(r)),
    field("Upgrade name", upgrade.text("name")),
    field("Upgrade height", content.has("software_upgrade") ? upgrade.int("height") : ""),
    field("Spend recipient", spend.text("recipient"), "address"),
    field("Spend amount", spend.norama("amount") ?? "", "norama"),
    field("Validator share", content.has("emission_split") ? `${split.uint("validator_percent")}%` : ""),
    field("Storage share", content.has("emission_split") ? `${split.uint("storage_percent")}%` : ""),
    field("Relay share", content.has("emission_split") ? `${split.uint("relay_percent")}%` : ""),
    field("Development share", content.has("emission_split") ? `${split.uint("development_percent")}%` : ""),
  ];
}

export const HOUSES_DECODERS = table(
  define("/orama.houses.v1.MsgSubmitProposal", {
    module: MODULE,
    tag: "Submit proposal",
    actor: "proposer",
    verb: ["submitted", "submit"],
    object: (r) => sp`a ${kindOf(r)} proposal`,
    fields: contentFields,
  }),
  define("/orama.houses.v1.MsgVoteToken", {
    module: MODULE,
    tag: "Vote with tokens",
    actor: "voter",
    verb: ["voted", "vote"],
    object: (r) => sp`${vote(r)} on ${proposal(r)} as a token holder`,
    fields: (r) => [field("Proposal", `#${r.uint("proposal_id")}`), field("Vote", vote(r))],
  }),
  define("/orama.houses.v1.MsgVoteOperator", {
    module: MODULE,
    tag: "Vote as operator",
    actor: "voter",
    verb: ["voted", "vote"],
    object: (r) => sp`${vote(r)} on ${proposal(r)} as a node operator`,
    fields: (r) => [field("Proposal", `#${r.uint("proposal_id")}`), field("Vote", vote(r))],
  }),
  define("/orama.houses.v1.MsgLockHouseBond", {
    module: MODULE,
    tag: "Lock house bond",
    category: "staking",
    actor: "signer",
    verb: ["locked", "lock"],
    object: (r) => sp`${r.amountPart("amount")} as a house bond`,
    amount: (r) => r.norama("amount"),
    fields: (r) => [field("Amount", r.norama("amount") ?? "", "norama")],
  }),
  define("/orama.houses.v1.MsgUnlockHouseBond", {
    module: MODULE,
    tag: "Unlock house bond",
    category: "staking",
    actor: "signer",
    verb: ["unlocked", "unlock"],
    object: () => ["their house bond"],
  }),
  define("/orama.houses.v1.MsgExecuteProposal", {
    module: MODULE,
    tag: "Execute proposal",
    actor: "signer",
    verb: ["executed", "execute"],
    object: (r) => sp`${proposal(r)}`,
    fields: (r) => [field("Proposal", `#${r.uint("proposal_id")}`)],
  }),
);
