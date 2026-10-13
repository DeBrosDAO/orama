import { define, field, plural, sp, table } from "./kit";
import type { Reader } from "./kit";

const ARCHIVE = { module: "Archive", category: "storage", actor: "archiver" } as const;

const range = (r: Reader) => `${r.int("start_height")} to ${r.int("end_height")}`;
const where = (r: Reader) => [field("Blocks", range(r)), field("Node ID", r.text("node_id"))];
const piece = (r: Reader) => [
  field("Piece size", r.bytes("piece_bytes")),
  field("Real leaves", r.uint("real_leaf_count")),
  field("Padded leaves", r.uint("padded_leaf_count")),
];

export const ARCHIVE_DECODERS = table(
  define("/orama.archive.v1.MsgAttest", {
    ...ARCHIVE,
    tag: "Attest archive",
    verb: ["attested", "attest"],
    object: (r) => sp`the archive bundle of blocks ${range(r)}`,
    fields: (r) => [...where(r), field("Bundle CID", r.text("bundle_cid")), ...piece(r)],
  }),
  define("/orama.archive.v1.MsgAttachReplicas", {
    ...ARCHIVE,
    tag: "Attach archive replicas",
    verb: ["attached", "attach"],
    object: (r) => sp`${plural(r.count("deal_ids"), "storage deal")} to the archive of blocks ${range(r)}`,
    fields: (r) => [...where(r), field("Deals", r.list("deal_ids"))],
  }),
  define("/orama.archive.v1.MsgCreateArchiveDeal", {
    ...ARCHIVE,
    tag: "Create archive deal",
    verb: ["opened", "open"],
    object: (r) => sp`an archive storage deal for blocks ${range(r)}`,
    fields: (r) => [...where(r), ...piece(r)],
  }),
);
