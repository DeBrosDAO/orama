import { AuthInfo, TxBody, TxRaw } from "./gen/cosmos/tx/v1beta1/tx";
import { addressFromPublicKey } from "./address";
import { PubKey } from "./gen/cosmos/crypto/secp256k1/keys";
import { formatCoins } from "./format";
import { MESSAGE_REGISTRY } from "./messages";
import { desc, type AnyMsg, type MsgDescription } from "./msg";

/** What an approval screen shows for a whole transaction. */
export interface TxDescription {
  /** The signer's address, or "" when the auth info carries no key. */
  signer: string;
  memo: string;
  fee: string;
  gasLimit: bigint;
  messages: Array<MsgDescription & { typeUrl: string }>;
  /** True when any message is sensitive, or any message type is unknown to this SDK. */
  sensitive: boolean;
}

/**
 * Describes one message. A type this registry does not hold is described as
 * unknown and sensitive, never skipped: a signer must not approve a message
 * it cannot read.
 */
export function describeMessage(msg: AnyMsg): MsgDescription & { typeUrl: string } {
  const def = MESSAGE_REGISTRY.get(msg.typeUrl);
  if (!def) {
    return {
      typeUrl: msg.typeUrl,
      ...desc("Unknown message", `Unknown message type ${msg.typeUrl} (${msg.value.length} bytes)`, [
        "This SDK cannot decode this message. Do not approve what you cannot read.",
      ], true),
    };
  }
  return { typeUrl: msg.typeUrl, ...def.describe(def.decode(msg.value)) };
}

/** Decodes a serialized TxRaw or a TxBody and AuthInfo pair into a description. */
export function describeTx(txBytes: Uint8Array): TxDescription {
  const raw = TxRaw.decode(txBytes);
  return describeParts(raw.bodyBytes, raw.authInfoBytes);
}

/** Describes a transaction from the two documents a SignDoc carries. */
export function describeParts(bodyBytes: Uint8Array, authInfoBytes: Uint8Array): TxDescription {
  const body = TxBody.decode(bodyBytes);
  const auth = AuthInfo.decode(authInfoBytes);
  const key = auth.signerInfos[0]?.publicKey;
  const messages = body.messages.map((m) => describeMessage({ typeUrl: m.typeUrl, value: m.value }));
  return {
    signer: key ? addressFromPublicKey(PubKey.decode(key.value).key) : "",
    memo: body.memo,
    fee: formatCoins(auth.fee?.amount),
    gasLimit: auth.fee?.gasLimit ?? 0n,
    messages,
    sensitive: messages.some((m) => m.sensitive),
  };
}
