import { describe, expect, it } from "vitest";
import { bech32Encode } from "../../model/bech32";
import { balanceChangesOf, feeOf, mapMessage, readIndexTx, summaryOf } from "./tx";
import type { Context } from "./tx";

const addr = (n: number) => bech32Encode("orama", new Uint8Array(20).fill(n));
const valoper = (n: number) => bech32Encode("oramavaloper", new Uint8Array(20).fill(n));
const HASH = "ab".repeat(32);

const ALICE = addr(1);
const BOB = addr(2);
const MODULE = addr(3);

const ctx: Context = {
  label: (a) => (a === ALICE ? "Alice" : undefined),
  validator: (operator) => ({ moniker: "val-one", operator }),
};

function raw(over: Record<string, unknown> = {}) {
  return {
    hash: HASH,
    height: 12,
    index: 0,
    code: 0,
    log: "",
    gas_wanted: 200000,
    gas_used: 81000,
    time: "2026-10-08T10:00:00.123456789Z",
    signer: ALICE,
    memo: "rent",
    messages: ["/cosmos.bank.v1beta1.MsgSend"],
    body: [{ "@type": "/cosmos.bank.v1beta1.MsgSend", from_address: ALICE, to_address: BOB, amount: [{ denom: "norama", amount: "5" }] }],
    events: [],
    ...over,
  };
}

const ev = (type: string, attrs: Record<string, string>) => ({ type, attributes: Object.entries(attrs).map(([key, value]) => ({ key, value })) });

describe("readIndexTx", () => {
  it("TestReadIndexTx_reads_a_transaction", () => {
    const tx = readIndexTx(raw());
    expect(tx.hash).toBe(HASH.toUpperCase());
    expect(tx.time).toBe("2026-10-08T10:00:00.123Z");
    expect(tx.signer).toBe(ALICE);
  });

  it("TestReadIndexTx_a_signer_less_transaction_has_no_signer", () => {
    expect(readIndexTx(raw({ signer: "" })).signer).toBeNull();
    expect(readIndexTx(raw({ signer: undefined })).signer).toBeNull();
  });

  it("TestReadIndexTx_rejects_malformed_records_without_echoing_them", () => {
    for (const bad of [{ hash: "xyz" }, { height: -1 }, { time: "yesterday" }, { signer: "orama1notanaddress" }, { body: "no" }, { messages: [1] }]) {
      expect(() => readIndexTx(raw(bad))).toThrow(/malformed/);
    }
    expect(() => readIndexTx(raw({ hash: "<script>" }))).toThrow(/^((?!script).)*$/);
  });

  it("TestReadIndexTx_strips_control_and_bidi_characters_from_the_memo", () => {
    expect(readIndexTx(raw({ memo: "pay‮evil\u0007" })).memo).toBe("payevil");
  });
});

describe("mapMessage", () => {
  it("TestMapMessage_send_of_norama", () => {
    const m = mapMessage(readIndexTx(raw()).body[0] ?? {}, null, ctx);
    expect(m).toEqual({ type: "send", from: { address: ALICE, label: "Alice" }, to: { address: BOB }, amount: "5" });
  });

  it("TestMapMessage_a_send_of_another_token_is_unknown_not_a_norama_amount", () => {
    const body = { "@type": "/cosmos.bank.v1beta1.MsgSend", from_address: ALICE, to_address: BOB, amount: [{ denom: "factory/x/gold", amount: "5" }] };
    expect(mapMessage(body, { address: ALICE }, ctx)).toEqual({ type: "unknown", typeUrl: "/cosmos.bank.v1beta1.MsgSend", signer: { address: ALICE } });
  });

  it("TestMapMessage_delegate_names_the_validators_operator_account", () => {
    const body = { "@type": "/cosmos.staking.v1beta1.MsgDelegate", delegator_address: ALICE, validator_address: valoper(7), amount: { denom: "norama", amount: "9" } };
    expect(mapMessage(body, null, ctx)).toEqual({
      type: "delegate",
      delegator: { address: ALICE, label: "Alice" },
      validator: { moniker: "val-one", operator: addr(7) },
      amount: "9",
    });
  });

  it("TestMapMessage_storage_deal_amount_is_price_times_replicas_times_epochs", () => {
    const body = {
      "@type": "/orama.storage.v1.MsgCreateDeal",
      signer: ALICE,
      granter: "",
      class: "DEAL_CLASS_PUBLIC_PIN",
      replicas: 3,
      price_per_epoch: "100",
      duration_epochs: "4",
    };
    expect(mapMessage(body, null, ctx)).toEqual({
      type: "storage_deal",
      owner: { address: ALICE, label: "Alice" },
      provider: null,
      amount: "1200",
      replicas: 3,
      visibility: "public",
    });
  });

  it("TestMapMessage_a_type_without_a_sentence_keeps_its_type_url", () => {
    const m = mapMessage({ "@type": "/orama.nodes.v1.MsgRegisterNode" }, null, ctx);
    expect(m).toEqual({ type: "unknown", typeUrl: "/orama.nodes.v1.MsgRegisterNode", signer: null });
  });

  it("TestMapMessage_a_known_type_with_a_broken_field_rejects_the_read", () => {
    const body = { "@type": "/cosmos.bank.v1beta1.MsgSend", from_address: "nope", to_address: BOB, amount: [{ denom: "norama", amount: "5" }] };
    expect(() => mapMessage(body, null, ctx)).toThrow(/malformed/);
  });
});

describe("feeOf and summaryOf", () => {
  it("TestFeeOf_reads_the_base_fee_and_tip_the_ante_handler_emitted", () => {
    const tx = readIndexTx(raw({ events: [ev("tx", { fee: "120norama", base_fee: "100", tip: "20" })] }));
    expect(feeOf(tx)).toEqual({ burned: "100", tip: "20", gasUsed: 81000, gasWanted: 200000 });
  });

  it("TestFeeOf_a_transaction_that_never_reached_the_fee_handler_paid_nothing", () => {
    expect(feeOf(readIndexTx(raw()))).toEqual({ burned: "0", tip: "0", gasUsed: 81000, gasWanted: 200000 });
  });

  it("TestFeeOf_ignores_a_fee_that_is_not_an_integer", () => {
    expect(feeOf(readIndexTx(raw({ events: [ev("tx", { base_fee: "1e9" })] }))).burned).toBe("0");
  });

  it("TestSummaryOf_a_failed_transaction_carries_a_clean_reason", () => {
    const s = summaryOf(readIndexTx(raw({ code: 5, log: "insufficient funds‮" })), ctx);
    expect(s.status).toEqual({ ok: false, reason: "insufficient funds" });
    expect(summaryOf(readIndexTx(raw({ code: 7, log: "" })), ctx).status).toEqual({ ok: false, reason: "the chain refused it (code 7)" });
  });

  it("TestSummaryOf_a_signer_less_transaction_has_no_signer", () => {
    const s = summaryOf(readIndexTx(raw({ signer: "", body: [{ "@type": "/orama.shielded.v1.MsgShieldedTransfer" }] })), ctx);
    expect(s.signer).toBeNull();
    expect(s.messages).toEqual([{ type: "unknown", typeUrl: "/orama.shielded.v1.MsgShieldedTransfer", signer: null }]);
  });
});

describe("balanceChangesOf", () => {
  it("TestBalanceChanges_net_per_account_and_the_burn_sum_to_zero", () => {
    const tx = readIndexTx(
      raw({
        events: [
          ev("coin_spent", { spender: ALICE, amount: "105norama" }),
          ev("coin_received", { receiver: BOB, amount: "5norama" }),
          ev("coin_received", { receiver: MODULE, amount: "100norama" }),
          ev("coin_spent", { spender: MODULE, amount: "100norama" }),
          ev("burn", { burner: MODULE, amount: "100norama" }),
        ],
      }),
    );
    const changes = balanceChangesOf(tx, ctx);
    expect(changes.map((c) => [c.party.kind === "wallet" ? c.party.ref.address : c.party.name, c.delta])).toEqual([
      [ALICE, "-105"],
      [BOB, "5"],
      ["burned", "100"],
    ]);
    expect(changes.reduce((n, c) => n + BigInt(c.delta), 0n)).toBe(0n);
    expect(changes.every((c) => c.before === null && c.after === null)).toBe(true);
  });

  it("TestBalanceChanges_other_denoms_and_junk_addresses_are_ignored", () => {
    const tx = readIndexTx(
      raw({
        events: [
          ev("coin_spent", { spender: ALICE, amount: "5factory/x/gold" }),
          ev("coin_received", { receiver: "cosmos1abc", amount: "5norama" }),
          ev("coin_received", { receiver: BOB, amount: "garbage" }),
        ],
      }),
    );
    expect(balanceChangesOf(tx, ctx)).toEqual([]);
  });

  it("TestBalanceChanges_a_mixed_coins_string_counts_only_norama", () => {
    const tx = readIndexTx(raw({ events: [ev("coin_received", { receiver: BOB, amount: "3factory/x/gold,7norama" })] }));
    expect(balanceChangesOf(tx, ctx).map((c) => c.delta)).toEqual(["7"]);
  });
});
