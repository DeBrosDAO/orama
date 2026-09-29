import type { ReactNode } from "react";
import { MESSAGE_TAGS, amountOf } from "../../model/describe";
import type { TxDetail } from "../../model/types";
import { formatInt, parseNorama } from "../../model/units";
import { Amount } from "../../ui/amount";
import { CopyChip } from "../../ui/copy";
import { Sentence } from "../../ui/sentence";
import { firstMessage, messageTypesText } from "./tx-messages";
import { messageTypeUrls } from "./tx-technical";

const ROW_GRID = "grid grid-cols-[110px_minmax(0,1fr)] gap-x-4 gap-y-2.5 text-[13px] sm:grid-cols-[150px_minmax(0,1fr)]";
const NONE = "None";
const NO_MESSAGES = "No messages";

function Rows({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className={ROW_GRID}>
      {rows.map(([term, value]) => (
        <div key={term} className="contents">
          <dt className="text-muted">{term}</dt>
          <dd className="m-0 [overflow-wrap:anywhere]">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Every message in the transaction, one row each: what it says and what kind it is. */
function ActionList({ tx }: { tx: TxDetail }) {
  if (tx.messages.length === 0) return <span className="text-muted">{NO_MESSAGES}</span>;
  return (
    <ol className="space-y-1.5">
      {tx.messages.map((m, i) => (
        <li key={`${m.type}-${i}`}>
          <Sentence message={m} failed={!tx.status.ok} />
          <span className="ml-2 text-xs text-muted">{MESSAGE_TAGS[m.type]}</span>
        </li>
      ))}
    </ol>
  );
}

/** The plain facts: what kind of transaction, how much, what it cost, what the sender wrote. */
export function DetailsPanel({ tx }: { tx: TxDetail }) {
  const first = firstMessage(tx.messages);
  const amount = first ? amountOf(first) : null;
  return (
    <Rows
      rows={[
        ["Type", messageTypesText(tx.messages) || NONE],
        ["Actions", <ActionList tx={tx} />],
        [
          tx.messages.length > 1 ? "Amount (first action)" : "Amount",
          amount === null ? (
            <span className="text-muted">None</span>
          ) : (
            <>
              <Amount norama={amount} />{" "}
              <span className="text-muted">({parseNorama(amount).toLocaleString("en-US")} norama)</span>
            </>
          ),
        ],
        ["Fee", <><Amount norama={tx.fee.burned} />, burned</>],
        ["Memo", tx.memo === "" ? <span className="text-muted">{NONE}</span> : tx.memo],
      ]}
    />
  );
}

function EventList({ events }: { events: TxDetail["events"] }) {
  if (events.length === 0) return <span className="text-muted">No events</span>;
  return (
    <ul className="space-y-1.5">
      {events.map((e, i) => (
        <li key={`${e.type}-${i}`}>
          <span className="font-mono">{e.type}</span>
          <span className="block font-mono text-xs text-muted">
            {Object.entries(e.attributes).map(([k, v]) => `${k}=${v}`).join("  ")}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** What a developer needs: type URLs, gas, events, and the raw body to copy. */
export function TechnicalPanel({ tx }: { tx: TxDetail }) {
  const urls = messageTypeUrls(tx.rawJson);
  return (
    <div className="space-y-3">
      <Rows
        rows={[
          [
            "Message type",
            urls.length === 0 ? (
              <span className="text-muted">Not in the raw body</span>
            ) : (
              <ul>{urls.map((u, i) => <li key={`${u}-${i}`} className="font-mono">{u}</li>)}</ul>
            ),
          ],
          ["Gas used", <span className="font-mono">{formatInt(tx.fee.gasUsed)} of {formatInt(tx.fee.gasWanted)}</span>],
          ["Events", <EventList events={tx.events} />],
        ]}
      />
      <div>
        <div className="mb-1.5 flex items-center justify-between gap-3 text-xs text-muted">
          <span>Raw transaction (JSON)</span>
          <CopyChip value={tx.rawJson} label="Copy raw transaction JSON" withText />
        </div>
        <pre
          tabIndex={0}
          aria-label="Raw transaction JSON"
          className="max-h-72 overflow-auto rounded-lg border border-border bg-bg p-3 font-mono text-xs leading-relaxed text-muted focus-visible:outline-2 focus-visible:outline-signal"
        >
          {tx.rawJson}
        </pre>
      </div>
    </div>
  );
}
