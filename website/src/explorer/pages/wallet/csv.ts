import { formatNorama, MINUS, parseNorama } from "../../model/units";
import type { ActivityItem } from "../../model/types";

export const CSV_HEADER = ["time", "direction", "type", "counterparty", "amount_ORAMA", "status", "tx_hash"] as const;

const LINE_BREAK = "\r\n";
const NEEDS_QUOTES = /[",\r\n]/;
/** Characters a spreadsheet reads as the start of a formula. */
const FORMULA_START = /^[=+\-@\t\r]/;

/** Quote a field per RFC 4180 when it holds a comma, quote or line break. */
function quote(field: string): string {
  return NEEDS_QUOTES.test(field) ? `"${field.replace(/"/g, '""')}"` : field;
}

/** Text a person or the chain wrote: a leading formula character is defused with a single quote. */
function textCell(value: string): string {
  return quote(FORMULA_START.test(value) ? `'${value}` : value);
}

/** A signed decimal with no digit grouping and an ASCII minus, so a spreadsheet reads it as a number. */
function amountCell(norama: string): string {
  parseNorama(norama);
  return formatNorama(norama).replace(/,/g, "").replace(MINUS, "-");
}

function statusText(item: ActivityItem): string {
  return item.status.ok ? "ok" : `failed: ${item.status.reason}`;
}

function rowFor(item: ActivityItem): string {
  const who = item.counterparty ? (item.counterparty.label ?? item.counterparty.address) : "";
  return [
    textCell(item.time),
    textCell(item.direction),
    textCell(item.message.type),
    textCell(who),
    amountCell(item.amount),
    textCell(statusText(item)),
    textCell(item.hash),
  ].join(",");
}

/**
 * The loaded activity as CSV (RFC 4180, CRLF). Every text cell is untrusted, so
 * each is neutralised against formula injection. The amount is generated from
 * a validated integer, never free text, so its leading "-" stays a minus sign.
 */
export function activityToCsv(items: ActivityItem[]): string {
  const lines = [CSV_HEADER.join(","), ...items.map(rowFor)];
  return lines.join(LINE_BREAK) + LINE_BREAK;
}

/** The download's name. */
export function csvFilename(address: string): string {
  return `${address}-activity.csv`;
}
