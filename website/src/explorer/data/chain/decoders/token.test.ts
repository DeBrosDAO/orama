import { describe, expect, it } from "vitest";
import { ALICE, BOB, CAROL, decode, fieldsOf, said } from "./fixtures";

const TOKEN = "/orama.token.v1.";
const GOLD = `factory/${ALICE}/gold`;

describe("x/token decoders", () => {
  it("TestToken_create_lists_the_capabilities_set_at_creation", () => {
    const m = decode(`${TOKEN}MsgCreateToken`, {
      creator: ALICE,
      subdenom: "gold",
      name: "Gold",
      symbol: "GLD",
      description: "a token",
      mint: true,
      freeze: true,
      transfer_fee_bps: 250,
      permanent_delegate: BOB,
    });
    expect(said(m)).toBe("Alice created the token GLD (“Gold”)");
    expect(fieldsOf(m)).toMatchObject({ Capabilities: "mint, freeze", "Transfer fee": "2.5%", "Permanent delegate": BOB, Symbol: "GLD" });
    expect(fieldsOf(decode(`${TOKEN}MsgCreateToken`, { creator: ALICE, subdenom: "g", name: "G" }))).not.toHaveProperty("Transfer fee");
  });

  it("TestToken_mint_burn_name_the_token_not_ORAMA", () => {
    const mint = decode(`${TOKEN}MsgMint`, { sender: ALICE, denom: GOLD, recipient: BOB, amount: "1500" });
    expect(said(mint)).toBe(`Alice minted 1,500 ${GOLD} to Bob`);
    expect(mint.amount).toBeNull();
    expect(said(decode(`${TOKEN}MsgBurn`, { sender: ALICE, denom: GOLD, amount: "7" }), true)).toBe(`Alice tried to burn 7 ${GOLD}`);
  });

  it("TestToken_a_token_message_in_norama_states_ORAMA", () => {
    const m = decode(`${TOKEN}MsgMint`, { sender: ALICE, denom: "norama", recipient: BOB, amount: "2000000000" });
    expect(said(m)).toBe("Alice minted 2 ORAMA to Bob");
    expect(m.amount).toBe("2000000000");
  });

  it("TestToken_transfer_with_and_without_a_from_account", () => {
    expect(said(decode(`${TOKEN}MsgTransfer`, { sender: ALICE, denom: GOLD, amount: "3", to: BOB }))).toBe(`Alice transferred 3 ${GOLD} to Bob`);
    const delegated = decode(`${TOKEN}MsgTransfer`, { sender: ALICE, from: CAROL, to: BOB, denom: GOLD, amount: "3" });
    expect(said(delegated)).toBe(`Alice transferred 3 ${GOLD} from Carol to Bob`);
    expect(delegated.category).toBe("transfers");
  });

  it("TestToken_freeze_pause_and_their_reversals", () => {
    expect(said(decode(`${TOKEN}MsgSetFrozen`, { sender: ALICE, denom: GOLD, account: BOB, frozen: true }))).toBe(`Alice froze Bob for ${GOLD}`);
    expect(said(decode(`${TOKEN}MsgSetFrozen`, { sender: ALICE, denom: GOLD, account: BOB, frozen: false }))).toBe(`Alice unfroze Bob for ${GOLD}`);
    expect(said(decode(`${TOKEN}MsgSetFrozen`, { sender: ALICE, denom: GOLD, account: BOB, frozen: true }), true)).toBe(`Alice tried to freeze Bob for ${GOLD}`);
    expect(said(decode(`${TOKEN}MsgSetPaused`, { sender: ALICE, denom: GOLD, paused: true }))).toBe(`Alice paused ${GOLD}`);
    expect(said(decode(`${TOKEN}MsgSetPaused`, { sender: ALICE, denom: GOLD, paused: false }))).toBe(`Alice unpaused ${GOLD}`);
  });

  it("TestToken_renounce_shieldable_delete", () => {
    expect(said(decode(`${TOKEN}MsgRenounce`, { sender: ALICE, denom: GOLD, extension: "EXTENSION_PERMANENT_DELEGATE" }))).toBe(
      `Alice renounced the permanent delegate extension of ${GOLD}`,
    );
    expect(said(decode(`${TOKEN}MsgSetShieldable`, { sender: ALICE, denom: GOLD }))).toBe(`Alice made ${GOLD} shieldable`);
    expect(said(decode(`${TOKEN}MsgDeleteToken`, { sender: ALICE, denom: GOLD }))).toBe(`Alice deleted the token ${GOLD}`);
  });

  it("TestToken_an_unreadable_amount_is_said_to_be_unreadable_not_zero", () => {
    expect(said(decode(`${TOKEN}MsgBurn`, { sender: ALICE, denom: GOLD, amount: "12abc" }))).toBe(`Alice burned (unreadable)`);
  });
});
