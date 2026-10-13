import type { StorageVisibility, TxMessage } from "../../../model/types";
import { account, digits, optStr, str, uint } from "../wire";
import type { Rec } from "../wire";
import { define, field, plural, sp, table, walletRef } from "./kit";
import type { Decoder, Env, Reader } from "./kit";

const MODULE = "Storage";
const TYPE_CREATE_DEAL = "/orama.storage.v1.MsgCreateDeal";
const DEAL_CLASS_PRIVATE = "DEAL_CLASS_PRIVATE";
const DEAL_CLASS_PUBLIC_PIN = "DEAL_CLASS_PUBLIC_PIN";

const RELEASE_REASONS = [
  ["UNSPECIFIED", "unspecified"],
  ["LEGAL", "legal"],
] as const;

const DEAL_CLASSES = [
  ["UNSPECIFIED", "unspecified"],
  ["PRIVATE", "private"],
  ["PUBLIC_PIN", "public"],
  ["ARCHIVE", "archive"],
] as const;

function visibilityOf(deal: string): StorageVisibility | null {
  if (deal.includes(DEAL_CLASS_PRIVATE)) return "private";
  if (deal.includes(DEAL_CLASS_PUBLIC_PIN)) return "public";
  return null;
}

/** A private or public deal as the storage-deal message; any other class has no such shape. */
function mapDeal(m: Rec, env: Env): TxMessage | null {
  const visibility = visibilityOf(str(m.class, "deal class", 64));
  if (visibility === null) return null;
  const payer = optStr(m.granter, "deal granter", 120) || str(m.signer, "deal signer", 120);
  const replicas = uint(m.replicas, "deal replicas");
  const price = BigInt(digits(m.price_per_epoch, "deal price"));
  const epochs = BigInt(digits(m.duration_epochs, "deal duration"));
  return {
    type: "storage_deal",
    owner: walletRef(account(payer, "deal payer"), env),
    provider: null,
    amount: (price * BigInt(replicas) * epochs).toString(),
    replicas,
    visibility,
  };
}

const deal = (r: Reader) => `deal #${r.uint("deal_id")}`;
const slot = (r: Reader) => `slot ${r.uint("slot")} of ${deal(r)}`;
const node = (r: Reader) => r.name("node_id");

/** The escrow of a deal: price per epoch times replicas times epochs, or null when a figure is unreadable. */
function escrow(r: Reader): string | null {
  const price = r.digits("price_per_epoch");
  const replicas = r.digits("replicas");
  const epochs = r.digits("duration_epochs");
  return price === null || replicas === null || epochs === null ? null : (BigInt(price) * BigInt(replicas) * BigInt(epochs)).toString();
}

const [, createDealAction] = define(TYPE_CREATE_DEAL, {
  module: MODULE,
  tag: "Create deal",
  category: "storage",
  actor: "signer",
  verb: ["opened", "open"],
  object: (r) => sp`a storage deal (${r.enumName("class", "DEAL_CLASS_", DEAL_CLASSES) || "unspecified"} class) with ${plural(Number(r.digits("replicas") ?? 0), "copy", "copies")} for ${r.uint("duration_epochs")} epochs`,
  amount: escrow,
  fields: (r) => [
    field("Class", r.enumName("class", "DEAL_CLASS_", DEAL_CLASSES)),
    field("Copies", r.uint("replicas")),
    field("Price per epoch", r.norama("price_per_epoch") ?? "", "norama"),
    field("Epochs", r.uint("duration_epochs")),
    field("Pieces", String(r.count("pieces"))),
  ],
});

const createDeal: Decoder = (m, env) => mapDeal(m, env) ?? createDealAction(m, env);

const STORAGE = { module: MODULE, category: "storage", actor: "signer" } as const;

export const STORAGE_DECODERS = table(
  [TYPE_CREATE_DEAL, createDeal],
  define("/orama.storage.v1.MsgExtendDeal", {
    ...STORAGE,
    tag: "Extend deal",
    verb: ["extended", "extend"],
    object: (r) => sp`${deal(r)} by ${r.uint("extra_epochs")} epochs`,
    fields: (r) => [field("Deal", deal(r)), field("Extra epochs", r.uint("extra_epochs"))],
  }),
  define("/orama.storage.v1.MsgGrantDealAuthorization", {
    ...STORAGE,
    tag: "Grant deal authorization",
    verb: ["authorized", "authorize"],
    object: (r) => sp`${r.party("grantee")} to open storage deals with a spend limit of ${r.amountPart("spend_limit")}`,
    fields: (r) => [
      field("Grantee", r.text("grantee"), "address"),
      field("Spend limit", r.norama("spend_limit") ?? "", "norama"),
      field("Period (epochs)", r.uint("period_epochs")),
      field("Largest piece", r.bytes("max_piece_bytes")),
      field("Longest deal (epochs)", r.uint("max_duration_epochs")),
      field("Copies", r.uint("replicas")),
      field("Expires at epoch", r.uint("expiry_epoch")),
    ],
  }),
  define("/orama.storage.v1.MsgRevokeDealAuthorization", {
    ...STORAGE,
    tag: "Revoke deal authorization",
    verb: ["revoked", "revoke"],
    object: (r) => sp`the deal authorization of ${r.party("grantee")}`,
    fields: (r) => [field("Grantee", r.text("grantee"), "address")],
  }),
  define("/orama.storage.v1.MsgAcceptDeal", {
    ...STORAGE,
    tag: "Accept deal",
    verb: ["accepted", "accept"],
    object: (r) => sp`${slot(r)} on node ${node(r)}`,
    fields: (r) => [field("Node ID", node(r)), field("Deal", deal(r)), field("Slot", r.uint("slot"))],
  }),
  define("/orama.storage.v1.MsgDeclineDeal", {
    ...STORAGE,
    tag: "Decline deal",
    verb: ["declined", "decline"],
    object: (r) => sp`${slot(r)} on node ${node(r)}`,
    fields: (r) => [field("Node ID", node(r)), field("Deal", deal(r)), field("Slot", r.uint("slot")), field("Reason", r.text("reason"))],
  }),
  define("/orama.storage.v1.MsgSubmitProofs", {
    ...STORAGE,
    tag: "Submit proofs",
    verb: ["submitted", "submit"],
    object: (r) => sp`${plural(r.count("proofs"), "storage proof")} for node ${node(r)}`,
    fields: (r) => [field("Node ID", node(r)), field("Proofs", String(r.count("proofs")))],
  }),
  define("/orama.storage.v1.MsgReleaseReplica", {
    ...STORAGE,
    tag: "Release replica",
    verb: ["released", "release"],
    object: (r) => sp`${slot(r)} from node ${node(r)}`,
    fields: (r) => [
      field("Node ID", node(r)),
      field("Deal", deal(r)),
      field("Slot", r.uint("slot")),
      field("Reason", r.enumName("reason", "RELEASE_REASON_", RELEASE_REASONS)),
    ],
  }),
);
