import { formatPct } from "../../model/units";
import { durationText } from "../../model/governance";
import type { ProposalContent } from "../../model/governance";
import type { MessageField } from "../../model/types";
import { ChainReadError } from "./client";
import { field, reader } from "./decoders/kit";
import type { Env, Reader } from "./decoders/kit";
import { decShare, legacyDec } from "./wire";
import type { Rec } from "./wire";

/** A proposal's content names accounts as text only: the reader here links nothing. */
const ENV: Env = { label: () => undefined, validator: (operator) => ({ moniker: operator, operator }), signer: null };

const percent = (r: Reader, key: string, what: string) => formatPct(decShare(r.raw(key), what));
const seconds = (r: Reader, key: string) => durationText(Number(r.digits(key) ?? "0"));
const share = (r: Reader, key: string) => `${r.uint(key)}%`;

type Facts = (r: Reader) => (MessageField | null)[];

/** The one action a proposal carries is the field of ProposalContent that is set; each has its own facts. */
const KINDS: readonly (readonly [key: string, label: string, facts: Facts])[] = [
  [
    "parameter_change",
    "Parameter change",
    (r) => [
      field("Token house quorum", percent(r, "token_quorum", "token quorum")),
      field("Token house pass threshold", percent(r, "token_pass_threshold", "pass threshold")),
      field("Voting period", seconds(r, "voting_period_seconds")),
      field("House bond", r.norama("house_bond") ?? "", "norama"),
      field("Eligible operators per /16", r.uint("max_eligible_per_prefix16")),
      field("Eligible operators per ASN", r.uint("max_eligible_per_asn")),
    ],
  ],
  ["software_upgrade", "Software upgrade", (r) => [field("Upgrade name", r.name("name")), field("At height", r.int("height"))]],
  [
    "emission_split",
    "Emission split change",
    (r) => [
      field("Validators", share(r, "validator_percent")),
      field("Storage", share(r, "storage_percent")),
      field("Relay", share(r, "relay_percent")),
      field("Development", share(r, "development_percent")),
    ],
  ],
  [
    "development_spend",
    "Development spend",
    (r) => [
      field("Recipient", r.text("recipient"), "address"),
      field("Amount", r.norama("amount") ?? "", "norama"),
      field("Emission epoch claimed", r.uint("epoch")),
    ],
  ],
  [
    "power_bounds",
    "Power bounds change",
    (r) => [field("M bound", String(legacyDec(r.raw("m_max"), "power bound"))), field("Activates M", r.bool("activate_m") ? "Yes" : "No")],
  ],
  ["relay_reporters", "Relay reporter change", (r) => [field("Add", r.list("add")), field("Remove", r.list("remove"))]],
  [
    "allow_list",
    "Allow-list change",
    (r) => [
      field("Code upload: add", r.list("code_upload_add")),
      field("Code upload: remove", r.list("code_upload_remove")),
      field("Adapters: add", r.list("adapter_add")),
      field("Adapters: remove", r.list("adapter_remove")),
    ],
  ],
];

/** The content of a proposal: its kind and what it would do. A content with no kind set is malformed, since the chain refuses one. */
export function readContent(content: Rec): ProposalContent {
  const r = reader(content, ENV);
  const kind = KINDS.find(([key]) => r.has(key));
  if (kind === undefined) throw new ChainReadError("The chain sent a malformed proposal content.");
  return { kind: kind[1], facts: kind[2](r.at(kind[0])).filter((f): f is MessageField => f !== null) };
}
