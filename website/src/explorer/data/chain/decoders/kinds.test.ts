import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { DECODERS } from "./index";
import { categoryTable } from "./kit";

/** The indexer's table of which message types are transfers, staking or storage (chain/indexer/kinds.go embeds it). */
const KINDS_FILE = path.resolve(import.meta.dirname, "../../../../../../chain/indexer/message_kinds.json");
const KINDS = new Set(["transfers", "staking", "storage"]);

function expected(): Record<string, string> {
  void DECODERS;
  return Object.fromEntries(
    [...categoryTable()].filter(([, category]) => KINDS.has(category)).sort(([a], [b]) => a.localeCompare(b)),
  );
}

describe("the indexer's message kinds", () => {
  it("TestKinds_the_indexer_filters_by_the_categories_the_decoders_show", () => {
    if (process.env.UPDATE_KINDS === "1") fs.writeFileSync(KINDS_FILE, `${JSON.stringify(expected(), null, 2)}\n`);
    const have = JSON.parse(fs.readFileSync(KINDS_FILE, "utf8")) as Record<string, string>;
    expect(have).toEqual(expected());
  });

  it("TestKinds_every_decoder_but_the_hand_written_one_has_a_category", () => {
    const table = categoryTable();
    // The shielded transfer carries no signer and is written by hand, with category "other".
    expect(Object.keys(DECODERS).filter((url) => !table.has(url))).toEqual(["/orama.shielded.v1.MsgShieldedTransfer"]);
  });

  it("TestKinds_a_send_is_a_transfer_and_a_delegate_is_staking", () => {
    const table = categoryTable();
    expect(table.get("/cosmos.bank.v1beta1.MsgSend")).toBe("transfers");
    expect(table.get("/cosmos.staking.v1beta1.MsgDelegate")).toBe("staking");
    expect(table.get("/orama.storage.v1.MsgCreateDeal")).toBe("storage");
  });
});
