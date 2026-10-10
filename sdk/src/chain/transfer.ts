import { SDKError } from "../errors";
import { isOramaAddress } from "./address";
import type { OramaChainClient, SignAndBroadcastOptions } from "./client";
import { BASE_DENOM, formatAmount } from "./format";
import { MSG } from "./messages";
import type { OramaSigner } from "./signer";

/**
 * Shown next to every public transfer. A caller that chooses `public: true` is told, in the result
 * and in the error text of a refusal, that the payment is visible to everyone.
 */
export const PUBLIC_TRANSFER_WARNING =
  "This is a public transfer: the sender, the recipient and the amount are visible on the chain to everyone, permanently.";

/** The largest amount of norama a transfer takes: the chain's integers are 256-bit; this is far beyond the supply. */
const MAX_AMOUNT_DIGITS = 38;

/** What a transfer asks for. */
export interface TransferRequest {
  /**
   * The recipient. For a public transfer an orama1... account; for a private one the shielded
   * address the {@link ShieldedTransferBuilder} understands.
   */
  to: string;
  /** Norama, as a positive whole number (1 ORAMA is 10^9 norama). */
  amount: bigint | number | string;
  /**
   * Pay in the open. Only the boolean `true` does: absent or `false` is a private transfer, so a
   * transfer cannot become public by omission. Any other value ("false", 1, null) is refused with a
   * TypeError instead of being read as true or as false.
   */
  public?: boolean;
}

/**
 * Builds the transactions of a private transfer from the owner's shielded notes. It proves with the
 * owner's shielded spending key, so it lives in the wallet, never in this SDK: RootWallet, or a
 * wallet that holds the key. The transactions it returns are signer-less and ready to broadcast, in
 * the order they must be sent.
 */
export interface ShieldedTransferBuilder {
  build(request: { to: string; amount: bigint }): Promise<Uint8Array[]>;
}

/** What a transfer needs besides the request. */
export interface TransferOptions {
  /** The shielded wallet for a private transfer. */
  shielded?: ShieldedTransferBuilder;
  /** The account that pays and signs, for a public transfer. */
  signer?: OramaSigner;
  /** Chain id, gas limit and fee for a public transfer's transaction. */
  tx?: SignAndBroadcastOptions;
}

/** A transfer that was sent privately. */
export interface PrivateTransferResult {
  privacy: "private";
  /** The hash of each transaction, in the order broadcast. */
  txHashes: string[];
}

/** A transfer that was sent publicly. */
export interface PublicTransferResult {
  privacy: "public";
  txHash: string;
  /** {@link PUBLIC_TRANSFER_WARNING}: surface it to the person who chose to pay publicly. */
  warning: string;
}

export type TransferResult = PrivateTransferResult | PublicTransferResult;

/**
 * Thrown for a private transfer that cannot be built: no shielded wallet was given. It is never
 * turned into a public transfer; the caller who wants one passes `public: true`.
 */
export class PrivateTransferUnavailableError extends SDKError {
  constructor() {
    super(
      "a private transfer needs a shielded wallet that builds the bundle (options.shielded), and none was given; " +
        "to pay publicly instead, pass public: true explicitly",
      400,
      "PRIVATE_TRANSFER_UNAVAILABLE",
    );
    this.name = "PrivateTransferUnavailableError";
  }
}

function positiveAmount(value: bigint | number | string): bigint {
  const text = typeof value === "number" ? (Number.isSafeInteger(value) ? String(value) : "") : String(value);
  if (!/^[1-9][0-9]*$/.test(text) || text.length > MAX_AMOUNT_DIGITS) {
    throw new RangeError(`amount ${String(value)} must be a positive whole number of ${BASE_DENOM}`);
  }
  return BigInt(text);
}

function requireBoolean(flag: unknown): boolean {
  if (flag === undefined) return false;
  if (typeof flag !== "boolean") {
    throw new TypeError(`public must be the boolean true or false, got ${JSON.stringify(flag)}`);
  }
  return flag;
}

/**
 * Sends ORAMA, privately unless `request.public` is `true`. See {@link TransferRequest}.
 *
 * Public: signs a bank send with `options.signer` and broadcasts it. Private: asks
 * `options.shielded` for the signer-less transactions and broadcasts them through the gateway.
 */
export async function transfer(
  chain: OramaChainClient,
  request: TransferRequest,
  options: TransferOptions = {},
): Promise<TransferResult> {
  const pay = requireBoolean(request.public);
  const amount = positiveAmount(request.amount);
  if (pay) return transferPublic(chain, request.to, amount, options);
  return transferPrivate(chain, request.to, amount, options);
}

async function transferPublic(
  chain: OramaChainClient,
  to: string,
  amount: bigint,
  options: TransferOptions,
): Promise<PublicTransferResult> {
  const { signer, tx } = options;
  if (!signer || !tx) throw new Error("a public transfer needs options.signer and options.tx (chain id, gas limit, fee)");
  if (!isOramaAddress(to)) throw new Error(`${to} is not an orama1... account address`);
  if (to === signer.address) throw new Error("the recipient is the sender: a payment to yourself moves nothing");
  const msg = MSG.bankSend.create({
    fromAddress: signer.address,
    toAddress: to,
    amount: [{ denom: BASE_DENOM, amount: amount.toString() }],
  });
  const { result } = await chain.signAndBroadcast([msg], signer, tx);
  return { privacy: "public", txHash: result.txHash, warning: PUBLIC_TRANSFER_WARNING };
}

async function transferPrivate(
  chain: OramaChainClient,
  to: string,
  amount: bigint,
  options: TransferOptions,
): Promise<PrivateTransferResult> {
  if (!options.shielded) throw new PrivateTransferUnavailableError();
  const txs = await options.shielded.build({ to, amount });
  if (txs.length === 0) throw new Error("the shielded wallet built no transaction");
  const txHashes: string[] = [];
  for (const tx of txs) txHashes.push((await chain.broadcastTx(tx)).txHash);
  return { privacy: "private", txHashes };
}

/**
 * Moves `amount` norama of the signer's earnings to the signer's own bank balance
 * (MsgWithdrawEarnings). The destination is not a parameter: the chain pays the signer. Withdrawing
 * is visible on the chain; what happens to the balance afterwards is the owner's choice: a public
 * transfer, or shielding it.
 */
export async function withdrawEarnings(
  chain: OramaChainClient,
  amount: bigint | number | string,
  signer: OramaSigner,
  tx: SignAndBroadcastOptions,
): Promise<{ txHash: string; withdrawn: string }> {
  const norama = positiveAmount(amount);
  const msg = MSG.feesWithdrawEarnings.create({ signer: signer.address, amount: norama.toString() });
  const { result } = await chain.signAndBroadcast([msg], signer, tx);
  return { txHash: result.txHash, withdrawn: formatAmount(norama) };
}
