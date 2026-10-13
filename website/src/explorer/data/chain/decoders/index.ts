import { ARCHIVE_DECODERS } from "./archive";
import { BANK_DECODERS } from "./bank";
import { CNFT_DECODERS } from "./cnft";
import { DISTRIBUTION_DECODERS } from "./distribution";
import { EMISSION_DECODERS } from "./emission";
import { FEEGRANT_DECODERS } from "./feegrant";
import { FEES_DECODERS } from "./fees";
import { HOUSES_DECODERS } from "./houses";
import { MARKET_DECODERS } from "./market";
import { NODES_DECODERS } from "./nodes";
import { PROTOCOL_DECODERS } from "./protocol";
import { RELAY_DECODERS } from "./relay";
import { SHIELDED_DECODERS } from "./shielded";
import { SLASHING_DECODERS } from "./slashing";
import { STAKING_DECODERS } from "./staking";
import { STORAGE_DECODERS } from "./storage";
import { TOKEN_DECODERS } from "./token";
import { WASM_DECODERS } from "./wasm";
import type { Decoder, DecoderTable } from "./kit";

export type { Context, Decoder, Env, LabelLookup, ValidatorLookup } from "./kit";
export { walletRef } from "./kit";

/**
 * Every message type the explorer reads, by type URL. The module files are
 * the Orama modules (nodes, storage, archive, relay, token, cnft, market,
 * houses, fees, emission, shielded), then the standard Cosmos modules the
 * chain wires (bank, staking, distribution, slashing, feegrant, upgrade,
 * evidence, consensus, auth) and CosmWasm. x/power has no message. The
 * completeness test fails when the chain registers a message missing here.
 */
export const DECODERS: DecoderTable = Object.freeze({
  ...NODES_DECODERS,
  ...STORAGE_DECODERS,
  ...ARCHIVE_DECODERS,
  ...RELAY_DECODERS,
  ...TOKEN_DECODERS,
  ...CNFT_DECODERS,
  ...MARKET_DECODERS,
  ...HOUSES_DECODERS,
  ...FEES_DECODERS,
  ...EMISSION_DECODERS,
  ...SHIELDED_DECODERS,
  ...BANK_DECODERS,
  ...STAKING_DECODERS,
  ...DISTRIBUTION_DECODERS,
  ...SLASHING_DECODERS,
  ...FEEGRANT_DECODERS,
  ...PROTOCOL_DECODERS,
  ...WASM_DECODERS,
});

/** The decoder for a type URL, or undefined for a type the explorer has none for. */
export function decoderFor(typeUrl: string): Decoder | undefined {
  return Object.hasOwn(DECODERS, typeUrl) ? DECODERS[typeUrl] : undefined;
}
