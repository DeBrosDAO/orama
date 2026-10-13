import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { bundleOf, isUnshield, privacyOf, privacyOfMessage } from "../../model/shielded";
import type { TxDetail, TxEvent, TxMessage } from "../../model/types";
import { TxRow } from "../../ui/tx-row";
import { PrivacyBadge } from "../../ui/privacy-badge";
import { PrivacyCard } from "./privacy-card";
import { privacyNotes } from "./privacy-notes";
import { DetailsPanel } from "./tx-panels";
import { TxVerdict } from "./tx-verdict";

const render = (node: React.ReactNode) => renderToStaticMarkup(<MemoryRouter>{node}</MemoryRouter>);
const SIGNER = { address: "orama1sx0x6xpv8wtgshrajsqgednruq26aes0h2jh07" };

const action = (typeUrl: string, tag: string): TxMessage => ({
  type: "action",
  typeUrl,
  module: "Shielded",
  tag,
  category: "other",
  actor: null,
  done: [{ kind: "text", text: `${tag} done` }],
  tried: [{ kind: "text", text: `${tag} tried` }],
  amount: null,
  fields: [],
});
const TRANSFER = action("/orama.shielded.v1.MsgShieldedTransfer", "Shielded transfer");
const SHIELD = action("/orama.shielded.v1.MsgShield", "Shield");
const UNSHIELD = action("/orama.shielded.v1.MsgUnshield", "Unshield");
const SEND: TxMessage = { type: "send", from: SIGNER, to: { address: "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqmg3rhc" }, amount: "5000000000" };

const bundle = (kind: string, actions: string, amount: string): TxEvent => ({ type: "shielded_bundle", attributes: { kind, actions, amount } });

function tx(messages: TxMessage[], events: TxEvent[] = [], over: Partial<TxDetail> = {}): TxDetail {
  return {
    hash: "AB".repeat(32),
    height: 90,
    time: "2026-10-08T12:00:00.000Z",
    status: { ok: true },
    signer: SIGNER,
    messages,
    fee: { burned: "0", tip: "0", gasUsed: 100, gasWanted: 200 },
    memo: "",
    balanceChanges: [],
    events,
    rawJson: "{}",
    context: { amountPercentile: null, priorBetweenParties: null, previousFromSigner: null, otherInBlock: 0 },
    ...over,
  };
}

describe("bundleOf", () => {
  it("TestBundleOf_reads_the_kind_the_actions_and_the_amount_of_the_event", () => {
    expect(bundleOf([bundle("unshield", "2", "9000")])).toEqual({ kind: "unshield", actions: 2, amount: "9000" });
    expect(bundleOf([{ type: "tx", attributes: {} }, bundle("transfer", "1", "0")])).toEqual({ kind: "transfer", actions: 1, amount: "0" });
  });

  it("TestBundleOf_no_event_or_a_malformed_one_is_null", () => {
    expect(bundleOf([])).toBeNull();
    expect(bundleOf([bundle("mystery", "1", "1")])).toBeNull();
    expect(bundleOf([bundle("shield", "-1", "1")])).toBeNull();
    expect(bundleOf([bundle("shield", "1", "1.5")])).toBeNull();
  });
});

describe("privacy of a transaction", () => {
  it("TestPrivacyOf_a_private_transfer_is_private_the_pool_edge_is_a_boundary_and_the_rest_is_public", () => {
    expect(privacyOfMessage(TRANSFER)).toBe("private");
    expect(privacyOfMessage(SHIELD)).toBe("boundary");
    expect(privacyOfMessage(UNSHIELD)).toBe("boundary");
    expect(privacyOfMessage(SEND)).toBe("public");
    expect(privacyOf([SEND, SHIELD])).toBe("boundary");
    expect(privacyOf([SHIELD, TRANSFER])).toBe("private");
    expect(privacyOf([])).toBe("public");
    expect(isUnshield(UNSHIELD)).toBe(true);
    expect(isUnshield(SHIELD)).toBe(false);
  });
});

describe("privacyNotes", () => {
  it("TestPrivacyNotes_a_public_transaction_has_none", () => {
    expect(privacyNotes(tx([SEND]))).toBeNull();
  });

  it("TestPrivacyNotes_a_private_transfer_shows_its_fee_and_never_a_sender_receiver_or_amount", () => {
    const notes = privacyNotes(tx([TRANSFER], [bundle("transfer", "2", "700000")], { signer: null }));
    expect(notes?.privacy).toBe("private");
    expect(notes?.shown.map((f) => f.label)).toEqual(["Result", "Actions in the bundle", "Fee, paid from the pool"]);
    expect(notes?.shown.find((f) => f.kind === "norama")?.value).toBe("700000");
    expect(notes?.hidden).toEqual(expect.arrayContaining(["Who sent it", "Who received it", "How much was sent"]));
  });

  it("TestPrivacyNotes_a_shield_and_an_unshield_show_the_public_signer_and_the_amount_that_crossed", () => {
    const shield = privacyNotes(tx([SHIELD], [bundle("shield", "1", "5000")]));
    expect(shield?.shown.map((f) => [f.label, f.value])).toEqual([
      ["Result", "Accepted by the chain"],
      ["Actions in the bundle", "1"],
      ["Signed by", SIGNER.address],
      ["Amount entering the pool", "5000"],
    ]);
    expect(shield?.hidden[0]).toMatch(/Who will own the shielded value/);
    const unshield = privacyNotes(tx([UNSHIELD], [bundle("unshield", "1", "8000")]));
    expect(unshield?.shown.some((f) => f.label === "Amount leaving the pool" && f.value === "8000")).toBe(true);
    expect(unshield?.hidden[0]).toBe("Which shielded notes paid for it");
  });

  it("TestPrivacyNotes_a_refused_transaction_shows_no_amount_and_says_nothing_moved", () => {
    const notes = privacyNotes(tx([SHIELD], [bundle("shield", "1", "5000")], { status: { ok: false, reason: "insufficient funds" } }));
    expect(notes?.shown.map((f) => f.label)).toEqual(["Result", "Signed by"]);
    expect(notes?.shown[0]?.value).toMatch(/nothing moved/);
  });
});

describe("rendering", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("TestPrivacyBadge_marks_private_and_boundary_and_leaves_public_bare_unless_asked", () => {
    expect(render(<PrivacyBadge privacy="private" />)).toContain("Private");
    expect(render(<PrivacyBadge privacy="boundary" />)).toContain("Crosses the shielded pool");
    expect(render(<PrivacyBadge privacy="public" />)).toBe("");
    expect(render(<PrivacyBadge privacy="public" showPublic />)).toContain("Public");
  });

  it("TestPrivacyCard_says_what_is_on_chain_and_what_is_not_and_links_the_pool_page", () => {
    const notes = privacyNotes(tx([TRANSFER], [bundle("transfer", "1", "400000")], { signer: null }));
    const html = render(<PrivacyCard notes={notes!} />);
    expect(html).toContain("A private transfer");
    expect(html).toContain("On chain");
    expect(html).toContain("Not on chain");
    expect(html).toContain("Who sent it");
    expect(html).toContain('href="/explorer/shielded"');
    expect(html).not.toContain(SIGNER.address);
  });

  it("TestTxVerdict_a_private_transfer_carries_the_private_badge_and_a_bank_send_the_public_one", () => {
    vi.stubGlobal("window", { location: { href: "https://orama.network/explorer/tx/AB" } });
    expect(render(<TxVerdict tx={tx([TRANSFER], [], { signer: null })} />)).toContain("Private");
    const send = render(<TxVerdict tx={tx([SEND])} />);
    expect(send).toContain("Public");
    expect(send).not.toContain("Private");
    expect(render(<TxVerdict tx={tx([action("/x", "Other")])} />)).not.toContain("Public<");
  });

  it("TestDetailsPanel_a_private_transfers_fee_is_the_bundles_not_a_zero_burn", () => {
    const html = render(<DetailsPanel tx={tx([TRANSFER], [bundle("transfer", "1", "400000")], { signer: null })} />);
    expect(html).toContain("paid from the shielded pool");
    expect(html).not.toContain(", burned");
    expect(render(<DetailsPanel tx={tx([SEND])} />)).toContain(", burned");
  });

  it("TestDetailsPanel_a_shield_states_the_amount_that_crossed_from_the_event_and_a_refused_one_states_none", () => {
    expect(render(<DetailsPanel tx={tx([SHIELD], [bundle("shield", "1", "5000000000")])} />)).toContain("5 ORAMA");
    const refused = render(<DetailsPanel tx={tx([SHIELD], [bundle("shield", "1", "5000000000")], { status: { ok: false, reason: "no funds" } })} />);
    expect(refused).not.toContain("5 ORAMA");
  });

  it("TestTxRow_a_private_transfer_row_says_private", () => {
    const row = render(
      <TxRow tx={{ hash: "AB".repeat(32), height: 1, time: "2026-10-08T12:00:00.000Z", status: { ok: true }, signer: null, messages: [TRANSFER], fee: { burned: "0", tip: "0", gasUsed: 0, gasWanted: 0 } }} />,
    );
    expect(row).toContain("Private");
  });
});
