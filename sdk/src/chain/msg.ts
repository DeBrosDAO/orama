import type { BinaryReader, BinaryWriter } from "@bufbuild/protobuf/wire";

/** One protobuf message packed the way a transaction body carries it. */
export interface AnyMsg {
  typeUrl: string;
  value: Uint8Array;
}

/** What an approval screen shows for one message. */
export interface MsgDescription {
  /** A short name of the action, for a list of messages. */
  title: string;
  /** One line saying what the message does, with every amount and address. */
  summary: string;
  /** Facts a signer must see before approving: powers granted, royalties, limits, expiry. */
  notes: string[];
  /** True when the message moves control rather than only funds: it is shown with a warning. */
  sensitive: boolean;
}

/** The part of a ts-proto message codec the chain module uses. */
export interface MsgCodec<T> {
  encode(message: T, writer?: BinaryWriter): BinaryWriter;
  decode(input: BinaryReader | Uint8Array, length?: number): T;
  fromJSON(object: unknown): T;
}

/** A message type with every field optional, at any depth. */
export type DeepPartial<T> = T extends Uint8Array | string | number | boolean | bigint
  ? T
  : T extends Array<infer U>
    ? Array<DeepPartial<U>>
    : T extends object
      ? { [K in keyof T]?: DeepPartial<T[K]> }
      : T;

/** One message type: its type URL, codec and approval description. */
export interface MsgDef<T> {
  readonly typeUrl: string;
  readonly codec: MsgCodec<T>;
  /** Packs value. Fields left out take their protobuf default, which is not encoded. */
  create(value: DeepPartial<T>): AnyMsg;
  decode(bytes: Uint8Array): T;
  describe(value: T): MsgDescription;
}

const desc = (title: string, summary: string, notes: string[] = [], sensitive = false): MsgDescription => ({
  title,
  summary,
  notes,
  sensitive,
});

export { desc };

/** Builds a MsgDef. `fromPartial` is read off the generated codec. */
export function defineMsg<T>(
  typeUrl: string,
  codec: MsgCodec<T> & { fromPartial(value: never): T },
  describe: (value: T) => MsgDescription,
): MsgDef<T> {
  return {
    typeUrl,
    codec,
    create(value) {
      const full = (codec.fromPartial as (v: unknown) => T)(value);
      return { typeUrl, value: codec.encode(full).finish() };
    },
    decode: (bytes) => codec.decode(bytes),
    describe,
  };
}
