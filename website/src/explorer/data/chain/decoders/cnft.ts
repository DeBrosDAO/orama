import { define, field, plural, sp, table } from "./kit";
import type { Reader } from "./kit";

const MODULE = "Compressed NFTs";
const SHORT_ASSET_BYTES = 8;
const ASSET_BYTES = 32;
const UNKNOWN_ASSET = "(unknown)";

/** The asset a message names, shortened: its leaf is in `key` (current for cnft, leaf for market). */
export const assetOf = (r: Reader, key: string) => `asset ${r.at(key).hex("asset_id", SHORT_ASSET_BYTES) || UNKNOWN_ASSET}`;
export const assetFields = (r: Reader, key: string) => [
  field("Tree", `#${r.uint("tree_id")}`),
  field("Asset ID", r.at(key).hex("asset_id", ASSET_BYTES)),
  field("Current owner", r.at(key).text("owner"), "address"),
];
const tree = (r: Reader) => `tree #${r.uint("tree_id")}`;

export const CNFT_DECODERS = table(
  define("/orama.cnft.v1.MsgCreateCollection", {
    module: MODULE,
    tag: "Create collection",
    actor: "creator",
    verb: ["created", "create"],
    object: (r) => sp`the NFT collection “${r.name("name")}” with a ${r.bps("royalty_bps")} royalty`,
    fields: (r) => [field("Name", r.text("name")), field("Royalty", r.bps("royalty_bps"))],
  }),
  define("/orama.cnft.v1.MsgCreateTree", {
    module: MODULE,
    tag: "Create NFT tree",
    actor: "creator",
    verb: ["created", "create"],
    object: (r) => sp`an NFT tree of depth ${r.uint("depth")} for collection #${r.uint("collection_id")}`,
    fields: (r) => [
      field("Collection", `#${r.uint("collection_id")}`),
      field("Depth", r.uint("depth")),
      field("Buffer", r.uint("buffer")),
      field("Canopy", r.uint("canopy")),
    ],
  }),
  define("/orama.cnft.v1.MsgMint", {
    module: MODULE,
    tag: "Mint NFTs",
    actor: "creator",
    verb: ["minted", "mint"],
    object: (r) => sp`${plural(r.count("leaves"), "compressed NFT")} into ${tree(r)}`,
    fields: (r) => [field("Tree", `#${r.uint("tree_id")}`), field("NFTs", String(r.count("leaves")))],
  }),
  define("/orama.cnft.v1.MsgTransfer", {
    module: MODULE,
    tag: "Transfer NFT",
    actor: "signer",
    verb: ["transferred", "transfer"],
    object: (r) => sp`${assetOf(r, "current")} in ${tree(r)} to ${r.party("new_owner")}`,
    fields: (r) => [...assetFields(r, "current"), field("New owner", r.text("new_owner"), "address"), field("New delegate", r.text("new_delegate"), "address")],
  }),
  define("/orama.cnft.v1.MsgBurn", {
    module: MODULE,
    tag: "Burn NFT",
    actor: "signer",
    verb: ["burned", "burn"],
    object: (r) => sp`${assetOf(r, "current")} in ${tree(r)}`,
    fields: (r) => assetFields(r, "current"),
  }),
  define("/orama.cnft.v1.MsgUpdateMetadata", {
    module: MODULE,
    tag: "Update NFT metadata",
    actor: "signer",
    verb: ["updated", "update"],
    object: (r) => sp`the metadata of ${assetOf(r, "current")} in ${tree(r)}`,
    fields: (r) => [...assetFields(r, "current"), field("New metadata CID", r.text("new_metadata_cid"))],
  }),
  define("/orama.cnft.v1.MsgDecompress", {
    module: MODULE,
    tag: "Decompress NFT",
    actor: "owner",
    verb: ["decompressed", "decompress"],
    object: (r) => sp`${assetOf(r, "current")} from ${tree(r)}`,
    fields: (r) => assetFields(r, "current"),
  }),
  define("/orama.cnft.v1.MsgCompress", {
    module: MODULE,
    tag: "Compress NFT",
    actor: "owner",
    verb: ["compressed", "compress"],
    object: (r) => sp`asset ${r.hex("asset_id", SHORT_ASSET_BYTES) || UNKNOWN_ASSET}`,
    fields: (r) => [field("Asset ID", r.hex("asset_id", ASSET_BYTES))],
  }),
  define("/orama.cnft.v1.MsgRecordSnapshot", {
    module: MODULE,
    tag: "Record NFT snapshot",
    actor: "creator",
    verb: ["recorded", "record"],
    object: (r) => sp`a snapshot of ${tree(r)} (CID ${r.name("cid")})`,
    fields: (r) => [field("Tree", `#${r.uint("tree_id")}`), field("CID", r.text("cid"))],
  }),
);
