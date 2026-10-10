import type { SentencePart } from "../../model/describe";
import { formatDayHeading } from "../../model/time";
import type { ActivityItem, TxMessage } from "../../model/types";

export interface DayGroup {
  /** The UTC day, "2026-09-29": stable across renders. */
  day: string;
  heading: string;
  items: ActivityItem[];
}

const DAY_LENGTH = 10;

/** Group a newest-first list into runs that share a UTC day. Order is kept. */
export function groupByDay(items: ActivityItem[], nowMs: number): DayGroup[] {
  const groups: DayGroup[] = [];
  for (const item of items) {
    const day = item.time.slice(0, DAY_LENGTH);
    const last = groups[groups.length - 1];
    if (last && last.day === day) last.items.push(item);
    else groups.push({ day, heading: formatDayHeading(item.time, nowMs), items: [item] });
  }
  return groups;
}

const t = (text: string): SentencePart => ({ kind: "text", text });

function failureTail(item: ActivityItem): SentencePart[] {
  return item.status.ok ? [] : [t(` · ${item.status.reason}`)];
}

function sendLabel(m: Extract<TxMessage, { type: "send" }>, item: ActivityItem): SentencePart[] {
  const failed = !item.status.ok;
  if (item.direction === "out") {
    return [t(failed ? "Tried to send to " : "Sent to "), { kind: "wallet", ref: m.to }];
  }
  return [t(failed ? "Payment failed from " : "Received from "), { kind: "wallet", ref: m.from }];
}

function storageLabel(m: Extract<TxMessage, { type: "storage_deal" }>, item: ActivityItem): SentencePart[] {
  const failed = !item.status.ok;
  if (item.direction === "out") {
    const verb = failed ? "Tried to open a " : "Opened a ";
    return m.provider ? [t(`${verb}${m.visibility} storage deal · `), { kind: "wallet", ref: m.provider }] : [t(`${verb}${m.visibility} storage deal`)];
  }
  const verb = failed ? "Failed storage deal from " : `Accepted a ${m.visibility} storage deal from `;
  return [t(verb), { kind: "wallet", ref: m.owner }];
}

function messageLabel(item: ActivityItem): SentencePart[] {
  const m = item.message;
  const failed = !item.status.ok;
  switch (m.type) {
    case "send":
      return sendLabel(m, item);
    case "delegate":
      return [t(failed ? "Tried to delegate to " : "Delegated to "), { kind: "validator", ref: m.validator }];
    case "undelegate":
      return [t(failed ? "Tried to unstake from " : "Started unstaking from "), { kind: "validator", ref: m.validator }];
    case "claim_rewards":
      return [t(failed ? "Tried to claim rewards from " : "Reward from "), { kind: "validator", ref: m.validator }];
    case "storage_deal":
      return storageLabel(m, item);
    case "unknown":
      return [t(`${failed ? "Failed " : "Sent "}${m.typeUrl.replace(/^\//, "")} message`)];
  }
}

/** One activity row as a sentence from the wallet's own side, in parts so names can be links. */
export function activityLabel(item: ActivityItem): SentencePart[] {
  return [...messageLabel(item), ...failureTail(item)];
}

/** The parts as plain text, for accessible names. */
export function partsToText(parts: SentencePart[]): string {
  return parts
    .map((p) => {
      switch (p.kind) {
        case "text":
          return p.text;
        case "wallet":
          return p.ref.label ?? p.ref.address;
        case "validator":
          return p.ref.moniker;
        case "amount":
          return p.norama;
      }
    })
    .join("");
}
