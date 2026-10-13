import { useState } from "react";
import type { QueryState } from "../../data/use-query";
import type { ValidatorEpoch, ValidatorHistory } from "../../model/types";
import { formatInt, formatNorama, formatPct } from "../../model/units";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Query, Skeleton } from "../../ui/query-states";
import { RelTime } from "../../ui/rel-time";
import { Sparkline } from "../../ui/sparkline";
import { Stat } from "../../ui/stat";
import { cn } from "../../../lib/utils";
import { jailSentence, powerSeries, slashReason, slashTotals, statusWord, windowUptime } from "./logic";

/** The epochs shown before "Show all". */
const EPOCHS_SHOWN = 8;
const UPTIME_TIP =
  "Signed blocks over signed plus missed, from the commits of the blocks in closed epochs. A block the validator did not sign (offline, or a nil vote) is missed. The epoch still open is not counted.";

const NOT_SEEN = "The chain indexer has not seen this validator yet. It appears once the epoch after the one it joined in has closed.";

function Uptime({ epochs }: { epochs: readonly ValidatorEpoch[] }) {
  const latest = epochs[0];
  const overall = windowUptime(epochs);
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      <Stat
        label={
          <>
            Uptime, last closed epoch
            <Help tip={UPTIME_TIP} />
          </>
        }
        value={latest === undefined || latest.uptime === null ? "–" : formatPct(latest.uptime)}
        detail={latest === undefined ? "No closed epoch yet" : `epoch ${formatInt(latest.epoch)}: ${formatInt(latest.missedBlocks)} of ${formatInt(latest.signedBlocks + latest.missedBlocks)} blocks missed`}
      />
      <Stat
        label={`Uptime, last ${overall.epochs} epoch${overall.epochs === 1 ? "" : "s"}`}
        value={overall.uptime === null ? "–" : formatPct(overall.uptime)}
        detail={`${formatInt(overall.missedBlocks)} blocks missed in all`}
      />
      <Stat label="Voting power, last closed epoch" value={latest === undefined ? "–" : formatInt(latest.cometPower)} detail="CometBFT units" />
    </div>
  );
}

function PowerHistory({ epochs }: { epochs: readonly ValidatorEpoch[] }) {
  const [all, setAll] = useState(false);
  const shown = all ? epochs : epochs.slice(0, EPOCHS_SHOWN);
  return (
    <Card
      title={
        <>
          Voting power and signing, by epoch
          <Help tip="One row per closed epoch. Power is in CometBFT's integer units, so rows compare with each other; the share of the total is on the validators page for today only." />
        </>
      }
    >
      {epochs.length === 0 ? (
        <p className="text-sm text-muted">No closed epoch yet.</p>
      ) : (
        <>
          <div className="mb-3 flex items-center gap-3 text-muted">
            <Sparkline values={powerSeries(epochs)} width={220} height={40} />
            <span className="text-xs">power over the last {epochs.length} closed epoch{epochs.length === 1 ? "" : "s"}, oldest to newest</span>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <caption className="sr-only">Closed epochs of this validator, newest first</caption>
              <thead>
                <tr className="text-[11px] uppercase tracking-[0.07em] text-muted">
                  <th scope="col" className="pb-2 pr-3 font-medium">Epoch</th>
                  <th scope="col" className="pb-2 pr-3 font-medium">Power</th>
                  <th scope="col" className="pb-2 pr-3 font-medium">Uptime</th>
                  <th scope="col" className="pb-2 pr-3 font-medium">Missed</th>
                  <th scope="col" className="pb-2 pr-3 font-medium">State</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((e) => (
                  <tr key={e.epoch} className="border-t border-border/60">
                    <th scope="row" className="py-2 pr-3 text-left font-mono font-normal tabular-nums">{formatInt(e.epoch)}</th>
                    <td className="py-2 pr-3 font-mono tabular-nums">{formatInt(e.cometPower)}</td>
                    <td className={cn("py-2 pr-3 font-mono tabular-nums", e.uptime !== null && e.uptime < 0.95 && "text-loss")}>{e.uptime === null ? "–" : formatPct(e.uptime)}</td>
                    <td className="py-2 pr-3 font-mono tabular-nums">{formatInt(e.missedBlocks)}</td>
                    <td className="py-2 pr-3">
                      {e.jailed ? <Badge tone="bad">Jailed</Badge> : <span className="text-muted">{statusWord(e.status)}</span>}
                      {e.slashes > 0 && <Badge tone="bad" className="ml-1.5">{e.slashes} slash{e.slashes === 1 ? "" : "es"}</Badge>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {epochs.length > EPOCHS_SHOWN && (
            <button type="button" onClick={() => setAll((a) => !a)} className="mt-3 cursor-pointer text-sm underline underline-offset-4 hover:text-fg" aria-expanded={all}>
              {all ? "Show fewer epochs" : `Show all ${epochs.length} epochs`}
            </button>
          )}
        </>
      )}
    </Card>
  );
}

function Slashing({ history }: { history: ValidatorHistory }) {
  const totals = slashTotals(history.slashes);
  return (
    <Card title="Slashing and jail history">
      {history.slashes.length === 0 && history.jails.length === 0 ? (
        <p className="text-sm text-muted">No slash and no jail period recorded since the index began.</p>
      ) : (
        <div className="space-y-4">
          {history.slashes.length > 0 && (
            <div>
              <p className="mb-1 text-sm font-medium">
                {totals.count} slash{totals.count === 1 ? "" : "es"}, {formatNorama(totals.burned)} ORAMA burned in all
              </p>
              <ul className="divide-y divide-border/60">
                {history.slashes.map((s) => (
                  <li key={`${s.height}-${s.reason}`} className="flex flex-wrap items-baseline justify-between gap-x-4 py-2 text-sm">
                    <span>
                      {slashReason(s.reason)}
                      <span className="ml-2 text-xs text-muted">block {formatInt(s.height)}, epoch {formatInt(s.epoch)}</span>
                    </span>
                    <span className="text-muted">
                      {formatNorama(s.burned)} ORAMA burned · <RelTime iso={s.time} />
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {history.jails.length > 0 && (
            <ul className="divide-y divide-border/60">
              {history.jails.map((j) => (
                <li key={`${j.jailedHeight ?? 0}-${j.unjailedHeight ?? 0}`} className="py-2 text-sm">
                  {jailSentence(j)}
                  {j.reason !== "" && <span className="ml-2 text-xs text-muted">{slashReason(j.reason)}</span>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </Card>
  );
}

function HistoryBody({ history }: { history: ValidatorHistory }) {
  return (
    <>
      <Uptime epochs={history.epochs} />
      <PowerHistory epochs={history.epochs} />
      <Slashing history={history} />
    </>
  );
}

/** The part of the page that needs the chain indexer: it says why it is missing instead of leaving the rest of the page out. */
export function ValidatorHistoryView({ state, onRetry }: { state: QueryState<ValidatorHistory | null>; onRetry: () => void }) {
  if (state.status === "error") {
    return (
      <Card title="Uptime and history">
        <div role="alert" className="space-y-2 text-sm">
          <p>
            Uptime, slashes, jail periods and the voting-power history come from the chain indexer, which this gateway could not read:{" "}
            <span className="text-muted">{state.error.message}</span>
          </p>
          <button type="button" onClick={onRetry} className="cursor-pointer rounded-lg border border-border bg-surface-2 px-3 py-1.5 hover:border-fg/30">
            Try again
          </button>
        </div>
      </Card>
    );
  }
  return (
    <Query state={state} onRetry={onRetry} loading={<Skeleton className="h-64 w-full" />}>
      {(history) =>
        history === null ? (
          <Card title="Uptime and history">
            <p className="text-sm text-muted">{NOT_SEEN}</p>
          </Card>
        ) : (
          <HistoryBody history={history} />
        )
      }
    </Query>
  );
}
