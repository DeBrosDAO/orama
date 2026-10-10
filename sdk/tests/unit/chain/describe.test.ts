import { describe, expect, it } from "vitest";
import {
  MESSAGE_REGISTRY,
  MSG,
  buildSignDoc,
  describeMessage,
  describeParts,
  formatAmount,
  formatBps,
  formatCoins,
} from "../../../src/chain";
import { Role } from "../../../src/chain/gen/orama/nodes/v1/nodes";
import { DealClass } from "../../../src/chain/gen/orama/storage/v1/storage";

const alice = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s";
const bob = "orama1gpq5ys6yg4rywjzfff95cn2wfag9z5jn2mvlr0";

describe("formatting", () => {
  it("shows norama as ORAMA with the exact norama in brackets", () => {
    expect(formatAmount("1500000000")).toBe("1.5 ORAMA (1500000000 norama)");
    expect(formatAmount("1")).toBe("0.000000001 ORAMA (1 norama)");
    expect(formatAmount("0")).toBe("0 ORAMA (0 norama)");
    expect(formatAmount(2000000000n)).toBe("2 ORAMA (2000000000 norama)");
  });

  it("leaves other denoms and non-numbers exactly as given", () => {
    expect(formatAmount("5", "factory/x/gold")).toBe("5 factory/x/gold");
    expect(formatAmount("1e9")).toBe("1e9 norama");
  });

  it("formats coin lists and basis points", () => {
    expect(formatCoins(undefined)).toBe("nothing");
    expect(formatCoins([])).toBe("nothing");
    expect(formatCoins([{ denom: "norama", amount: "1000000000" }, { denom: "x", amount: "2" }])).toBe(
      "1 ORAMA (1000000000 norama), 2 x",
    );
    expect(formatBps(250)).toBe("2.5%");
    expect(formatBps(0)).toBe("0%");
    expect(formatBps(10000)).toBe("100%");
    expect(formatBps(5)).toBe("0.05%");
  });
});

describe("every message has a decoder", () => {
  it("describes the empty message of every type without throwing", () => {
    for (const [url, def] of MESSAGE_REGISTRY) {
      const packed = def.create({});
      const d = describeMessage(packed);
      expect(d.title, url).not.toBe("Unknown message");
      expect(d.summary.length, url).toBeGreaterThan(0);
      expect(d.typeUrl).toBe(url);
    }
  });

  it("round-trips through the registry's own decoder", () => {
    const packed = MSG.bankSend.create({ fromAddress: alice, toAddress: bob, amount: [{ denom: "norama", amount: "7" }] });
    const decoded = MSG.bankSend.decode(packed.value);
    expect(decoded.fromAddress).toBe(alice);
    expect(decoded.amount).toEqual([{ denom: "norama", amount: "7" }]);
  });
});

describe("approval details", () => {
  it("marks a message this SDK cannot read as unknown and sensitive", () => {
    const d = describeMessage({ typeUrl: "/evil.v1.MsgDrain", value: new Uint8Array([1, 2, 3]) });
    expect(d.title).toBe("Unknown message");
    expect(d.sensitive).toBe(true);
    expect(d.summary).toContain("/evil.v1.MsgDrain");
  });

  it("shows every power a new token grants, prominently", () => {
    const d = describeMessage(
      MSG.tokenCreate.create({
        creator: alice, subdenom: "gold", name: "Gold", symbol: "GLD",
        mint: true, freeze: true, pause: true, permanentDelegate: bob, transferFeeBps: 250, transferHook: bob,
      }),
    );
    const powers = d.notes[0];
    expect(powers).toContain("mint more supply");
    expect(powers).toContain("freeze any holder");
    expect(powers).toContain("pause all transfers");
    expect(powers).toContain(`${bob} can move any holder's tokens`);
    expect(powers).toContain("2.5% fee");
    expect(powers).toContain(`every transfer runs the contract ${bob}`);
    expect(d.sensitive).toBe(true);
  });

  it("does not warn about a token with no powers", () => {
    const d = describeMessage(MSG.tokenCreate.create({ creator: alice, subdenom: "plain", name: "P", symbol: "P" }));
    expect(d.notes[0]).toBe("Powers: none");
    expect(d.sensitive).toBe(false);
  });

  it("flags a delegate-signed token transfer", () => {
    const own = describeMessage(MSG.tokenTransfer.create({ sender: alice, from: alice, to: bob, denom: "d", amount: "1" }));
    const delegated = describeMessage(MSG.tokenTransfer.create({ sender: bob, from: alice, to: bob, denom: "d", amount: "1" }));
    expect(own.sensitive).toBe(false);
    expect(delegated.sensitive).toBe(true);
    expect(delegated.notes[0]).toContain("permanent delegate");
  });

  it("shows a DealAuthorization's limits and expiry", () => {
    const d = describeMessage(
      MSG.storageGrantDealAuthorization.create({
        signer: alice, grantee: bob, spendLimit: "3000000000", periodEpochs: 4n,
        maxPieceBytes: 1048576n, maxDurationEpochs: 52n, replicas: 3, expiryEpoch: 900n,
      }),
    );
    expect(d.summary).toContain("3 ORAMA");
    expect(d.summary).toContain("4-epoch period");
    expect(d.notes.join(" ")).toContain("Expires at epoch 900");
    expect(d.notes.join(" ")).toContain("1048576 bytes per piece");
    expect(d.sensitive).toBe(true);
  });

  it("shows the most a deal can cost", () => {
    const d = describeMessage(
      MSG.storageCreateDeal.create({ signer: alice, class: DealClass.DEAL_CLASS_PRIVATE, replicas: 3, pricePerEpoch: "1000", durationEpochs: 10n }),
    );
    expect(d.notes[0]).toContain("30000 norama");
  });

  it("marks MsgUpdateNode sensitive and lists what changes", () => {
    const d = describeMessage(MSG.nodesUpdateNode.create({ operator: alice, nodeId: "n1", hotKey: bob, setAsn: true, asn: 64512 }));
    expect(d.sensitive).toBe(true);
    expect(d.summary).toContain(`hot key becomes ${bob}`);
    expect(d.summary).toContain("ASN becomes 64512");
  });

  it("names the roles of a node registration", () => {
    const d = describeMessage(MSG.nodesRegisterNode.create({ operator: alice, nodeId: "n1", roles: [Role.ROLE_VALIDATOR, Role.ROLE_STORAGE] }));
    expect(d.summary).toContain("ROLE_VALIDATOR, ROLE_STORAGE");
  });

  it("shows a collection's royalty", () => {
    const d = describeMessage(MSG.cnftCreateCollection.create({ creator: alice, name: "Art", royaltyBps: 500 }));
    expect(d.notes[0]).toBe("Royalty on every sale: 5%");
  });

  it("shows the decoded JSON body and the funds of a contract call", () => {
    const body = new TextEncoder().encode('{"transfer":{"to":"x"}}');
    const d = describeMessage(
      MSG.wasmExecute.create({ sender: alice, contract: bob, msg: body, funds: [{ denom: "norama", amount: "1000000000" }] }),
    );
    expect(d.summary).toContain('{"transfer":{"to":"x"}}');
    expect(d.notes[0]).toContain("1 ORAMA");
    expect(d.sensitive).toBe(true);
  });

  it("does not hide a contract body that is not JSON", () => {
    const d = describeMessage(MSG.wasmExecute.create({ sender: alice, contract: bob, msg: new Uint8Array([0xff, 0xfe]) }));
    expect(d.summary).toContain("(not JSON) fffe");
  });

  it("describes a whole transaction from its sign document", () => {
    const signer = new Uint8Array(33).fill(2);
    signer[0] = 2;
    const doc = buildSignDoc(
      {
        chainId: "c", accountNumber: 1, sequence: 0, gasLimit: 100000, feeNorama: 1500000000, memo: "hi",
        msgs: [MSG.bankSend.create({ fromAddress: alice, toAddress: bob, amount: [{ denom: "norama", amount: "1" }] })],
      },
      signer,
    );
    const d = describeParts(doc.bodyBytes, doc.authInfoBytes);
    expect(d.memo).toBe("hi");
    expect(d.fee).toBe("1.5 ORAMA (1500000000 norama)");
    expect(d.gasLimit).toBe(100000n);
    expect(d.messages).toHaveLength(1);
    expect(d.sensitive).toBe(false);
  });
});
