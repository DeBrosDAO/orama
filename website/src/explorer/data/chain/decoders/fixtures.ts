import { expect } from "vitest";
import { bech32Encode } from "../../../model/bech32";
import { describeMessage } from "../../../model/describe";
import type { TxMessage } from "../../../model/types";
import { messageText } from "../../../ui/message-text";
import { mapMessage } from "../tx";
import type { Context } from "../tx";
import { readRegistered } from "./test-support";

export const addr = (n: number) => bech32Encode("orama", new Uint8Array(20).fill(n));
export const valoper = (n: number) => bech32Encode("oramavaloper", new Uint8Array(20).fill(n));

export const ALICE = addr(1);
export const BOB = addr(2);
export const CAROL = addr(3);

export const ctx: Context = {
  label: (a) => ({ [ALICE]: "Alice", [BOB]: "Bob", [CAROL]: "Carol" })[a],
  validator: (operator) => ({ moniker: "val-one", operator }),
};

const registered = readRegistered();

export type Action = Extract<TxMessage, { type: "action" }>;

/**
 * A message body as the indexer serves it: the chain's all-zero protojson of
 * the type with `fields` set. A field the chain's message does not have is a
 * mistake in the test, so it fails here instead of passing silently.
 */
export function body(typeUrl: string, fields: Record<string, unknown> = {}): Record<string, unknown> {
  const shape = registered.shapes[typeUrl];
  expect(shape, `${typeUrl} is not a message the chain registers`).toBeDefined();
  for (const key of Object.keys(fields)) expect(Object.keys(shape ?? {}), `${typeUrl} has no field ${key}`).toContain(key);
  return { ...shape, ...fields };
}

/** Decodes a message the way a transaction page does, and requires a decoded action. */
export function decode(typeUrl: string, fields: Record<string, unknown> = {}): Action {
  const m = mapMessage(body(typeUrl, fields), { address: ALICE, label: "Alice" }, ctx);
  expect(m.type, `${typeUrl} should decode`).toBe("action");
  return m as Action;
}

/** The sentence as plain text: "Alice registered node n1". */
export function said(m: TxMessage, failed = false): string {
  return messageText(describeMessage(m, failed));
}

export function fieldsOf(m: Action): Record<string, string> {
  return Object.fromEntries(m.fields.map((f) => [f.label, f.value]));
}
