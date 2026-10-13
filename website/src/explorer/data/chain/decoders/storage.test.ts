import { describe, expect, it } from "vitest";
import { mapMessage } from "../tx";
import { ALICE, BOB, body, ctx, decode, fieldsOf, said } from "./fixtures";

const STORAGE = "/orama.storage.v1.";
const ARCHIVE = "/orama.archive.v1.";
const RELAY = "/orama.relay.v1.";

describe("x/storage decoders", () => {
  it("TestStorage_a_private_deal_is_still_the_storage_deal_message", () => {
    const m = mapMessage(
      body(`${STORAGE}MsgCreateDeal`, { signer: ALICE, class: "DEAL_CLASS_PRIVATE", replicas: 3, price_per_epoch: "100", duration_epochs: "4" }),
      null,
      ctx,
    );
    expect(m).toMatchObject({ type: "storage_deal", amount: "1200", replicas: 3, visibility: "private" });
  });

  it("TestStorage_an_archive_deal_is_an_action_with_its_escrow", () => {
    const m = decode(`${STORAGE}MsgCreateDeal`, { signer: ALICE, class: "DEAL_CLASS_ARCHIVE", replicas: 3, price_per_epoch: "100", duration_epochs: "4" });
    expect(said(m)).toBe("Alice opened a storage deal (archive class) with 3 copies for 4 epochs");
    expect(m.amount).toBe("1200");
    expect(m.category).toBe("storage");
  });

  it("TestStorage_extend_and_authorizations", () => {
    expect(said(decode(`${STORAGE}MsgExtendDeal`, { signer: ALICE, deal_id: "12", extra_epochs: "30" }))).toBe("Alice extended deal #12 by 30 epochs");
    const grant = decode(`${STORAGE}MsgGrantDealAuthorization`, {
      signer: ALICE,
      grantee: BOB,
      spend_limit: "5000000000",
      period_epochs: "7",
      max_piece_bytes: "1048576",
      max_duration_epochs: "100",
      replicas: 3,
      expiry_epoch: "900",
    });
    expect(said(grant)).toBe("Alice authorized Bob to open storage deals with a spend limit of 5 ORAMA");
    expect(grant.amount).toBeNull();
    expect(fieldsOf(grant)["Largest piece"]).toBe("1 MiB (1,048,576 bytes)");
    expect(said(decode(`${STORAGE}MsgRevokeDealAuthorization`, { signer: ALICE, grantee: BOB }))).toBe("Alice revoked the deal authorization of Bob");
  });

  it("TestStorage_provider_messages", () => {
    const accept = decode(`${STORAGE}MsgAcceptDeal`, { signer: ALICE, node_id: "n1", deal_id: "9", slot: 2 });
    expect(said(accept)).toBe("Alice accepted slot 2 of deal #9 on node n1");
    const decline = decode(`${STORAGE}MsgDeclineDeal`, { signer: ALICE, node_id: "n1", deal_id: "9", slot: 2, reason: "disk full" });
    expect(said(decline, true)).toBe("Alice tried to decline slot 2 of deal #9 on node n1");
    expect(fieldsOf(decline).Reason).toBe("disk full");
    expect(said(decode(`${STORAGE}MsgSubmitProofs`, { signer: ALICE, node_id: "n1", proofs: [{}, {}, {}] }))).toBe("Alice submitted 3 storage proofs for node n1");
    expect(said(decode(`${STORAGE}MsgSubmitProofs`, { signer: ALICE, node_id: "n1", proofs: [{}] }))).toBe("Alice submitted 1 storage proof for node n1");
    const release = decode(`${STORAGE}MsgReleaseReplica`, { signer: ALICE, node_id: "n1", deal_id: "9", slot: 1, reason: "RELEASE_REASON_LEGAL" });
    expect(said(release)).toBe("Alice released slot 1 of deal #9 from node n1");
    expect(fieldsOf(release).Reason).toBe("legal");
  });
});

describe("x/archive decoders", () => {
  it("TestArchive_attest_names_the_block_range", () => {
    const m = decode(`${ARCHIVE}MsgAttest`, {
      archiver: ALICE,
      start_height: "1000",
      end_height: "1999",
      bundle_cid: "bafybundle",
      node_id: "n1",
      piece_bytes: "2048",
    });
    expect(said(m)).toBe("Alice attested the archive bundle of blocks 1,000 to 1,999");
    expect(fieldsOf(m)).toMatchObject({ Blocks: "1,000 to 1,999", "Bundle CID": "bafybundle", "Piece size": "2 KiB (2,048 bytes)" });
  });

  it("TestArchive_attach_and_create_deal", () => {
    expect(said(decode(`${ARCHIVE}MsgAttachReplicas`, { archiver: ALICE, start_height: "1", end_height: "10", deal_ids: ["5", "6", "7"] }))).toBe(
      "Alice attached 3 storage deals to the archive of blocks 1 to 10",
    );
    expect(said(decode(`${ARCHIVE}MsgCreateArchiveDeal`, { archiver: ALICE, start_height: "1", end_height: "10" }), true)).toBe(
      "Alice tried to open an archive storage deal for blocks 1 to 10",
    );
  });
});

describe("x/relay decoders", () => {
  it("TestRelay_register_relay_marks_an_exit", () => {
    const m = decode(`${RELAY}MsgRegisterRelay`, { operator: ALICE, node_id: "n1", exit: true, rsa_fingerprint: "AQID" });
    expect(said(m)).toBe("Alice registered an exit relay on node n1");
    expect(fieldsOf(m)).toMatchObject({ Kind: "Exit relay", "RSA fingerprint": "010203" });
    expect(said(decode(`${RELAY}MsgRegisterRelay`, { operator: ALICE, node_id: "n1" }))).toBe("Alice registered a relay on node n1");
  });

  it("TestRelay_update_relay_names_the_node_and_the_new_fingerprint", () => {
    const m = decode(`${RELAY}MsgUpdateRelay`, { operator: ALICE, node_id: "n1", rsa_fingerprint: "AQID" });
    expect(said(m)).toBe("Alice rotated the fingerprint of the relay on node n1");
    expect(fieldsOf(m)).toMatchObject({ "RSA fingerprint": "010203" });
  });

  it("TestRelay_report_epoch_counts_the_observations", () => {
    const m = decode(`${RELAY}MsgReportEpoch`, { reporter: ALICE, epoch: "42", chunk_index: 0, chunk_count: 2, entries: [{}, {}] });
    expect(said(m)).toBe("Alice reported 2 relay observations for epoch 42");
    expect(fieldsOf(m)).toMatchObject({ Epoch: "42", "Chunk count": "2", Observations: "2" });
  });

  it("TestRelay_update_reporters_lists_a_bounded_number", () => {
    const reporters = Array.from({ length: 9 }, (_, i) => `rep${i}`);
    const m = decode(`${RELAY}MsgUpdateReporters`, { signer: ALICE, reporters });
    expect(said(m)).toBe("Alice set the relay reporter list to 9 accounts");
    expect(fieldsOf(m).Reporters).toBe("rep0, rep1, rep2, rep3, rep4, rep5, +3 more");
    expect(said(decode(`${RELAY}MsgUpdateReporters`, { signer: ALICE, reporters: [] }))).toBe("Alice set the relay reporter list to 0 accounts");
  });
});
