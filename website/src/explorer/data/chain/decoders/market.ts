import { assetFields, assetOf } from "./cnft";
import { define, field, sp, table } from "./kit";
import type { Reader } from "./kit";

const MODULE = "Market";
const listing = (r: Reader) => `listing #${r.uint("listing_id")}`;
const bid = (r: Reader) => `bid #${r.uint("bid_id")}`;

export const MARKET_DECODERS = table(
  define("/orama.market.v1.MsgList", {
    module: MODULE,
    tag: "List NFT",
    actor: "seller",
    verb: ["listed", "list"],
    object: (r) => sp`${assetOf(r, "leaf")} from tree #${r.uint("tree_id")} for ${r.amountPart("price")}`,
    fields: (r) => [...assetFields(r, "leaf"), field("Price", r.norama("price") ?? "", "norama")],
  }),
  define("/orama.market.v1.MsgCancelListing", {
    module: MODULE,
    tag: "Cancel listing",
    actor: "seller",
    verb: ["cancelled", "cancel"],
    object: (r) => sp`${listing(r)}`,
    fields: (r) => [field("Listing", `#${r.uint("listing_id")}`)],
  }),
  define("/orama.market.v1.MsgBid", {
    module: MODULE,
    tag: "Bid",
    actor: "bidder",
    verb: ["bid", "bid"],
    object: (r) => sp`${r.amountPart("amount")} on ${listing(r)}`,
    fields: (r) => [field("Listing", `#${r.uint("listing_id")}`), field("Bid", r.norama("amount") ?? "", "norama")],
  }),
  define("/orama.market.v1.MsgCancelBid", {
    module: MODULE,
    tag: "Cancel bid",
    actor: "bidder",
    verb: ["cancelled", "cancel"],
    object: (r) => sp`${bid(r)} on ${listing(r)}`,
    fields: (r) => [field("Listing", `#${r.uint("listing_id")}`), field("Bid", `#${r.uint("bid_id")}`)],
  }),
  define("/orama.market.v1.MsgSettle", {
    module: MODULE,
    tag: "Settle sale",
    actor: "signer",
    verb: ["settled", "settle"],
    object: (r) => sp`${bid(r)} on ${listing(r)}`,
    fields: (r) => [field("Listing", `#${r.uint("listing_id")}`), field("Bid", `#${r.uint("bid_id")}`), field("Asset ID", r.at("leaf").hex("asset_id", 32))],
  }),
);
