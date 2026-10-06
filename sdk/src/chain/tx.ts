import { Any } from "./gen/google/protobuf/any";
import { PubKey } from "./gen/cosmos/crypto/secp256k1/keys";
import { SignMode } from "./gen/cosmos/tx/signing/v1beta1/signing";
import { AuthInfo, SignDoc, TxBody, TxRaw } from "./gen/cosmos/tx/v1beta1/tx";
import { BASE_DENOM } from "./format";
import type { AnyMsg } from "./msg";
import { verifyDirectSignature, type OramaSigner } from "./signer";

/** The secp256k1 public key type URL an AuthInfo signer carries. */
export const SECP256K1_PUBKEY_TYPE_URL = "/cosmos.crypto.secp256k1.PubKey";

/** A transaction to sign with SIGN_MODE_DIRECT. */
export interface UnsignedTx {
  chainId: string;
  accountNumber: bigint | number | string;
  sequence: bigint | number | string;
  gasLimit: bigint | number | string;
  /** The fee, in norama. The chain accepts no other fee denom. */
  feeNorama: bigint | number | string;
  /** Exactly one signer's messages: the chain's builder refuses a transaction with several. */
  msgs: readonly AnyMsg[];
  memo?: string;
}

/** The bytes of a transaction before its signature. */
export interface SignDocument {
  bodyBytes: Uint8Array;
  authInfoBytes: Uint8Array;
  /** The serialized SignDoc: what a signer signs SHA-256 of. */
  signDoc: Uint8Array;
}

export interface SignedTx extends SignDocument {
  signature: Uint8Array;
  /** The serialized TxRaw, ready to broadcast. */
  txBytes: Uint8Array;
}

const big = (v: bigint | number | string): bigint => BigInt(v);

function assertBuildable(tx: UnsignedTx): void {
  if (tx.chainId === "") throw new Error("chain id is empty");
  if (tx.msgs.length === 0) throw new Error("transaction has no messages");
  const fee = big(tx.feeNorama);
  if (fee <= 0n) throw new Error(`fee must be a positive amount of ${BASE_DENOM}`);
}

/**
 * Builds the TxBody, the AuthInfo and the SignDoc for tx signed by publicKey.
 * The encoding is the same bytes chain/client/tx builds in Go; a shared
 * fixture in the tests pins that.
 */
export function buildSignDoc(tx: UnsignedTx, publicKey: Uint8Array): SignDocument {
  assertBuildable(tx);
  const bodyBytes = TxBody.encode(
    TxBody.fromPartial({
      messages: tx.msgs.map((m) => Any.fromPartial({ typeUrl: m.typeUrl, value: m.value })),
      memo: tx.memo ?? "",
    }),
  ).finish();

  const authInfoBytes = AuthInfo.encode(
    AuthInfo.fromPartial({
      signerInfos: [
        {
          publicKey: Any.fromPartial({
            typeUrl: SECP256K1_PUBKEY_TYPE_URL,
            value: PubKey.encode(PubKey.fromPartial({ key: publicKey })).finish(),
          }),
          modeInfo: { single: { mode: SignMode.SIGN_MODE_DIRECT } },
          sequence: big(tx.sequence),
        },
      ],
      fee: { amount: [{ denom: BASE_DENOM, amount: big(tx.feeNorama).toString() }], gasLimit: big(tx.gasLimit) },
    }),
  ).finish();

  const signDoc = SignDoc.encode(
    SignDoc.fromPartial({
      bodyBytes,
      authInfoBytes,
      chainId: tx.chainId,
      accountNumber: big(tx.accountNumber),
    }),
  ).finish();
  return { bodyBytes, authInfoBytes, signDoc };
}

/** Joins a signature to the document it signs: the serialized TxRaw. */
export function assembleTx(doc: SignDocument, signature: Uint8Array): Uint8Array {
  if (signature.length !== 64) throw new Error(`signature is ${signature.length} bytes, want 64`);
  return TxRaw.encode(
    TxRaw.fromPartial({ bodyBytes: doc.bodyBytes, authInfoBytes: doc.authInfoBytes, signatures: [signature] }),
  ).finish();
}

/**
 * Builds tx, has signer sign it, and returns the encoded transaction. The
 * signature is checked against the signer's own public key before it is
 * used, so a signer that signs the wrong document or as another account
 * is an error here and not a rejected broadcast.
 */
export async function signTx(tx: UnsignedTx, signer: OramaSigner): Promise<SignedTx> {
  const doc = buildSignDoc(tx, signer.publicKey);
  const signature = await signer.signDirect(doc.signDoc);
  if (!verifyDirectSignature(signer.publicKey, doc.signDoc, signature)) {
    throw new Error(`the signer for ${signer.address} returned a signature that does not verify over the transaction`);
  }
  return { ...doc, signature, txBytes: assembleTx(doc, signature) };
}

/**
 * Verifies an encoded transaction the way the chain does: one signer, the
 * signature valid over the SignDoc rebuilt from the body, the auth info, the
 * chain id and the account number. Returns the signer's public key.
 */
export function verifyTx(txBytes: Uint8Array, chainId: string, accountNumber: bigint | number | string): Uint8Array {
  const raw = TxRaw.decode(txBytes);
  const auth = AuthInfo.decode(raw.authInfoBytes);
  if (auth.signerInfos.length !== 1 || raw.signatures.length !== 1) {
    throw new Error(`expected one signer, got ${auth.signerInfos.length} and ${raw.signatures.length} signatures`);
  }
  const info = auth.signerInfos[0];
  if (info.modeInfo?.single?.mode !== SignMode.SIGN_MODE_DIRECT) throw new Error("the signature is not SIGN_MODE_DIRECT");
  if (info.publicKey?.typeUrl !== SECP256K1_PUBKEY_TYPE_URL) throw new Error("the signer is not a secp256k1 key");
  const publicKey = PubKey.decode(info.publicKey.value).key;
  const signDoc = SignDoc.encode(
    SignDoc.fromPartial({
      bodyBytes: raw.bodyBytes,
      authInfoBytes: raw.authInfoBytes,
      chainId,
      accountNumber: big(accountNumber),
    }),
  ).finish();
  if (!verifyDirectSignature(publicKey, signDoc, raw.signatures[0])) {
    throw new Error("the signature does not verify for this chain id and account number");
  }
  return publicKey;
}
