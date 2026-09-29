/**
 * The Orama chain module: read the chain, build transactions, sign them with a
 * wallet, and describe them for an approval screen.
 *
 *     import { OramaChainClient, LocalSigner, MSG } from "@debros/orama/chain";
 *
 * The protobuf types under ./gen are generated from chain/proto by
 * `pnpm gen:chain`; they are not exported wholesale, only what a caller needs.
 */
export { OramaChainClient } from "./client";
export type {
  BroadcastResult,
  ChainAccount,
  ChainClientConfig,
  ChainQueryResult,
  PageOptions,
  QueryOptions,
  SignAndBroadcastOptions,
  Uint64Like,
} from "./client";

export { LocalSigner, verifyDirectSignature } from "./signer";
export type { OramaSigner } from "./signer";

export { addressFromPublicKey, addressToBytes, isOramaAddress } from "./address";

export { assembleTx, buildSignDoc, signTx, verifyTx, SECP256K1_PUBKEY_TYPE_URL } from "./tx";
export type { SignDocument, SignedTx, UnsignedTx } from "./tx";

export { MSG, MESSAGE_REGISTRY } from "./messages";
export type { AnyMsg, DeepPartial, MsgDef, MsgDescription } from "./msg";

export { describeMessage, describeParts, describeTx } from "./describe";
export type { TxDescription } from "./describe";

export {
  BASE_DENOM,
  BECH32_PREFIX,
  DENOM_DECIMALS,
  DISPLAY_DENOM,
  formatAmount,
  formatBps,
  formatCoins,
  fromHex,
  toHex,
} from "./format";
