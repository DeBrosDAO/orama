import { bech32Hrp, bech32Rehrp } from "../../../model/bech32";
import { formatNorama } from "../../../model/units";
import type { MessageCategory, MessageField, Norama, SentencePart, TxMessage, ValidatorRef, WalletRef } from "../../../model/types";
import { clean } from "../text";
import type { Rec } from "../wire";
import { MAX_RAW, base64Size, bpsText, bytesText, digitsOf, enumText, grouped, hexOf } from "./format";

/** Maps a validator operator address (oramavaloper...) to the validator's reference, or to a bare one. */
export type ValidatorLookup = (operator: string) => ValidatorRef;
/** The label a wallet carries, if the chain's validator directory names it. */
export type LabelLookup = (address: string) => string | undefined;

export interface Context {
  label: LabelLookup;
  validator: ValidatorLookup;
}

/** What a decoder needs: the lookups, and who signed the transaction. */
export interface Env extends Context {
  signer: WalletRef | null;
}

/** Reads one message body. Null means the explorer has no sentence for this body (it stays "unknown"). */
export type Decoder = (m: Rec, env: Env) => TxMessage | null;
export type DecoderTable = Readonly<Record<string, Decoder>>;

const ACCOUNT_HRP = "orama";
const OPERATOR_HRP = "oramavaloper";
const NORAMA = "norama";
/** The longest text a sentence or a field carries from the chain. */
export const MAX_TEXT = 120;
/** The most entries of a repeated field a sentence lists; the rest are counted. */
export const MAX_LISTED = 6;
const MAX_SCANNED = 64;
const MAX_ADDRESS = 80;
const HASH_BYTES = 32;
const MAX_DENOM = 100;
const UNREADABLE = "(unreadable)";
const EMPTY = "(empty)";
const NO_ACTOR = "An unnamed account";

export type Piece = string | SentencePart | readonly SentencePart[];

/** "1 deal", "3 deals". */
export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
}

export function walletRef(address: string, ctx: Context): WalletRef {
  const label = ctx.label(address);
  return label === undefined ? { address } : { address, label };
}

export const text = (value: string): SentencePart => ({ kind: "text", text: value });
export const amount = (norama: Norama): SentencePart => ({ kind: "amount", norama });

/** Pieces as sentence parts, with neighbouring text joined. */
export function parts(pieces: readonly Piece[]): SentencePart[] {
  const out: SentencePart[] = [];
  for (const part of pieces.flatMap((p) => (typeof p === "string" ? [text(p)] : "kind" in p ? [p] : p))) {
    const last = out[out.length - 1];
    if (part.kind === "text" && last?.kind === "text") out[out.length - 1] = text(last.text + part.text);
    else if (!(part.kind === "text" && part.text === "")) out.push(part);
  }
  return out;
}

/** A sentence from a template: strings become text, parts stay links. */
export function sp(strings: TemplateStringsArray, ...values: Piece[]): SentencePart[] {
  return parts(strings.flatMap((s, i) => (i < values.length ? [s, values[i] as Piece] : [s])));
}

/** A field, or null when there is nothing to say. */
export function field(label: string, value: string, kind: MessageField["kind"] = "text"): MessageField | null {
  return value === "" ? null : { label, value, kind };
}

/** The account a valoper address belongs to, or null when it is not a valid one. */
export function operatorAccount(valoper: string): string | null {
  return bech32Rehrp(valoper, ACCOUNT_HRP);
}

export interface Who {
  part: SentencePart;
  ref: WalletRef | null;
}

export interface Reader {
  /** Cleaned, bounded text; "" when absent. */
  text(key: string, max?: number): string;
  /** Like text, but "(empty)" when absent, for a name inside a sentence. */
  name(key: string): string;
  /** An unsigned integer with digit grouping; "0" when absent, "(unreadable)" when not a number. */
  uint(key: string): string;
  /** A signed integer with digit grouping (a block height); "0" when absent. */
  int(key: string): string;
  /** How many entries a repeated field holds. */
  count(key: string): number;
  /** An integer as plain digits, or null when it is not one. Absent is "0". */
  digits(key: string, signed?: boolean): string | null;
  bool(key: string): boolean;
  /** The entries of a repeated field, at most the first 64. */
  items(key: string): unknown[];
  /** A repeated string field as "a, b, c, +2 more"; "" when empty. */
  list(key: string): string;
  /** The account in a field: its signer fallback is the transaction's signer. */
  who(key: string): Who;
  /** An account or validator address as a link, any other text as text. */
  party(key: string): SentencePart;
  /** A field as the message holds it, unchecked: for a reader that validates it itself. */
  raw(key: string): unknown;
  /** True when a nested message is set (protojson writes an unset one as null). */
  has(key: string): boolean;
  /** A nested message, read the same way; empty when absent. */
  at(key: string): Reader;
  /** Bytes (base64 on the wire) as hex, cut to `maxBytes`; "" when absent. */
  hex(key: string, maxBytes?: number): string;
  /** The size of a bytes field (base64 on the wire), without decoding it. */
  blobSize(key: string): string;
  /** A byte count as "1.5 GiB (1,610,612,736 bytes)"; "(unreadable)" when it is not a number. */
  bytes(key: string): string;
  /** Basis points as a percentage: 250 is "2.5%". */
  bps(key: string): string;
  /** A Math.Int field in norama, or null when it is not an integer. */
  norama(key: string): Norama | null;
  amountPart(key: string): SentencePart;
  coins(key: string): { parts: SentencePart[]; norama: Norama | null };
  /** The coins as plain text for a field; "" when there are none. */
  coinsText(key: string): string;
  /** An amount of a token: ORAMA for norama, the cleaned denom otherwise. */
  token(denomKey: string, amountKey: string): SentencePart;
  enumName(key: string, prefix: string, names: readonly (readonly [string, string])[]): string;
}

function address(value: string, env: Env): Who {
  const lower = value.toLowerCase();
  const hrp = value.length <= MAX_ADDRESS ? bech32Hrp(lower) : null;
  if (hrp === ACCOUNT_HRP) {
    const ref = walletRef(lower, env);
    return { part: { kind: "wallet", ref }, ref };
  }
  const account = hrp === OPERATOR_HRP ? operatorAccount(lower) : null;
  if (account !== null) return { part: { kind: "validator", ref: env.validator(account) }, ref: walletRef(account, env) };
  return { part: text(clean(value, MAX_ADDRESS) || EMPTY), ref: null };
}

function str(m: Rec, key: string): string {
  const v = m[key];
  return typeof v === "string" ? v.slice(0, MAX_RAW) : "";
}

function tokenPart(denom: string, digits: string | null): SentencePart {
  if (digits === null) return text(UNREADABLE);
  return denom === NORAMA ? amount(digits) : text(`${grouped(digits)} ${clean(denom, MAX_DENOM) || EMPTY}`);
}

function plainText(list: readonly SentencePart[]): string {
  return list.map((p) => (p.kind === "text" ? p.text : p.kind === "amount" ? `${formatNorama(p.norama)} ORAMA` : "")).join("");
}

export function reader(m: Rec, env: Env): Reader {
  const digits = (key: string, signed = false) => (m[key] === undefined || m[key] === null ? "0" : digitsOf(m[key], signed));
  const items = (key: string): unknown[] => (Array.isArray(m[key]) ? (m[key] as unknown[]).slice(0, MAX_SCANNED) : []);
  const norama = (key: string): Norama | null => digitsOf(m[key]);
  const coins = (key: string) => {
    const out: Piece[] = [];
    let total = 0n;
    let any = false;
    for (const coin of items(key).slice(0, MAX_LISTED)) {
      const c = typeof coin === "object" && coin !== null ? (coin as Rec) : {};
      const denom = str(c, "denom");
      const d = digitsOf(c.amount);
      if (denom === NORAMA && d !== null) {
        total += BigInt(d);
        any = true;
      }
      if (out.length > 0) out.push(", ");
      out.push(tokenPart(denom, d));
    }
    return { parts: out.length === 0 ? [text("no funds")] : parts(out), norama: any ? total.toString() : null };
  };
  return {
    text: (key, max = MAX_TEXT) => clean(str(m, key), max),
    name: (key) => clean(str(m, key), MAX_TEXT) || EMPTY,
    uint: (key) => {
      const d = digits(key);
      return d === null ? UNREADABLE : grouped(d);
    },
    int: (key) => {
      const d = digits(key, true);
      return d === null ? UNREADABLE : grouped(d);
    },
    count: (key) => (Array.isArray(m[key]) ? (m[key] as unknown[]).length : 0),
    digits,
    bool: (key) => m[key] === true,
    items,
    raw: (key) => m[key],
    has: (key) => typeof m[key] === "object" && m[key] !== null && !Array.isArray(m[key]),
    at: (key) => reader(typeof m[key] === "object" && m[key] !== null && !Array.isArray(m[key]) ? (m[key] as Rec) : {}, env),
    blobSize: (key) => bytesText(base64Size(m[key])),
    hex: (key, maxBytes = HASH_BYTES) => hexOf(m[key], maxBytes),
    bytes: (key) => {
      const d = digits(key);
      return d === null ? UNREADABLE : bytesText(d);
    },
    bps: (key) => {
      const d = digits(key);
      return d === null ? UNREADABLE : bpsText(d);
    },
    list: (key) => {
      const all = Array.isArray(m[key]) ? (m[key] as unknown[]) : [];
      const shown = items(key)
        .slice(0, MAX_LISTED)
        .map((v) => (typeof v === "string" ? clean(v.slice(0, MAX_RAW), MAX_ADDRESS) : ""))
        .filter((v) => v !== "");
      const more = all.length - shown.length;
      return shown.length === 0 ? "" : `${shown.join(", ")}${more > 0 ? `, +${more} more` : ""}`;
    },
    who: (key) => {
      // An empty key is a message with no account field: the transaction's signer acts.
      const raw = key === "" ? "" : str(m, key);
      if (raw === "") return env.signer ? { part: { kind: "wallet", ref: env.signer }, ref: env.signer } : { part: text(NO_ACTOR), ref: null };
      return address(raw, env);
    },
    party: (key) => {
      const raw = str(m, key);
      return raw === "" ? text("(none)") : address(raw, env).part;
    },
    norama,
    amountPart: (key) => {
      const n = norama(key);
      return n === null ? text(UNREADABLE) : amount(n);
    },
    coins,
    coinsText: (key) => (items(key).length === 0 ? "" : plainText(coins(key).parts)),
    token: (denomKey, amountKey) => tokenPart(str(m, denomKey), digitsOf(m[amountKey])),
    enumName: (key, prefix, names) => enumText(m[key], prefix, names),
  };
}

export type Verb = readonly [string, string];

export interface Spec {
  /** The module that owns the message: "Nodes". */
  module: string;
  /** The short name of the action: "Register node". */
  tag: string;
  category?: MessageCategory;
  /** The message field that names the account acting. */
  actor: string;
  /** The past tense, then the infinitive that follows "tried to"; a function when the message decides the verb. */
  verb: Verb | ((r: Reader) => Verb);
  /** What the verb acts on, written after it. */
  object: (r: Reader) => Piece[];
  fields?: (r: Reader) => (MessageField | null)[];
  /** The norama the message states, when it states any. */
  amount?: (r: Reader) => Norama | null;
}

/**
 * The category each message type is shown under, recorded as the decoders are defined. The indexer
 * filters its transaction lists by the same categories (chain/indexer/message_kinds.json); a test
 * holds that file to this table.
 */
const categories = new Map<string, MessageCategory>();

/** The category of every message type defined so far, by type URL. */
export function categoryTable(): ReadonlyMap<string, MessageCategory> {
  return categories;
}

/** A decoder for one message type, built from its sentence and facts. */
export function define(typeUrl: string, spec: Spec): readonly [string, Decoder] {
  categories.set(typeUrl, spec.category ?? "other");
  const decode: Decoder = (m, env) => {
    const r = reader(m, env);
    const who = r.who(spec.actor);
    const verb = typeof spec.verb === "function" ? spec.verb(r) : spec.verb;
    const object = parts(spec.object(r));
    const tail = object.length === 0 ? [] : [text(" "), ...object];
    return {
      type: "action",
      typeUrl,
      module: spec.module,
      tag: spec.tag,
      category: spec.category ?? "other",
      actor: who.ref,
      done: parts([who.part, ` ${verb[0]}`, ...tail]),
      tried: parts([who.part, ` tried to ${verb[1]}`, ...tail]),
      amount: spec.amount?.(r) ?? null,
      fields: (spec.fields?.(r) ?? []).filter((f): f is MessageField => f !== null),
    };
  };
  return [typeUrl, decode];
}

/** A parameter change only a module's authority can make: the sentence names the authority and what it changes. */
export function paramsUpdate(typeUrl: string, module: string, what: string): readonly [string, Decoder] {
  return define(typeUrl, {
    module,
    tag: "Update parameters",
    actor: "authority",
    verb: ["updated", "update"],
    object: () => [`the ${what}`],
  });
}

export function table(...entries: (readonly [string, Decoder])[]): DecoderTable {
  return Object.fromEntries(entries);
}
