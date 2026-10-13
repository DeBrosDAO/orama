import type { QueryState } from "../../data/use-query";
import type { Counterparty, WalletRef } from "../../model/types";
import { formatSigned, parseNorama, shortAddress } from "../../model/units";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Identicon } from "../../ui/identicon";
import { Query, Skeleton } from "../../ui/query-states";
import { cn } from "../../../lib/utils";
import { COUNTERPARTY_LIMIT } from "./constants";

const TIP =
  "The wallets this account sends to or receives from most, among its 100 most recent transactions. Click one to see only your history with them.";
const PERCENT = 100n;
const ROW_SKELETON_CLASS = "h-12 w-full";

/** Each volume as a whole percent of the largest, so the bars compare with each other. */
function barPercents(list: Counterparty[]): number[] {
  const volumes = list.map((c) => parseNorama(c.volume));
  const max = volumes.reduce((a, b) => (b > a ? b : a), 0n);
  return volumes.map((v) => (max === 0n ? 0 : Number((v * PERCENT) / max)));
}

function netText(net: string): string {
  const s = formatSigned(net, 2);
  return `${s.sign}${s.text}`;
}

export interface CounterpartiesCardProps {
  state: QueryState<Counterparty[]>;
  onRetry: () => void;
  selected: WalletRef | null;
  onToggle: (who: WalletRef) => void;
}

function CounterpartyRow({ c, percent, pressed, onToggle }: { c: Counterparty; percent: number; pressed: boolean; onToggle: () => void }) {
  const name = c.ref.label ?? shortAddress(c.ref.address);
  return (
    <li>
      <button
        type="button"
        aria-pressed={pressed}
        aria-label={`${name}: ${c.txCount} ${c.txCount === 1 ? "transaction" : "transactions"}, net ${netText(c.net)} ORAMA. Show only history with them`}
        onClick={onToggle}
        className={cn(
          "flex w-full cursor-pointer items-center gap-3 rounded-lg border px-2 py-2 text-left transition-colors",
          pressed ? "border-signal/50 bg-surface-2" : "border-transparent hover:bg-surface-2",
        )}
      >
        <Identicon seed={c.ref.address} size={26} />
        <span className="min-w-0 flex-1">
          <span className={cn("block truncate text-sm font-medium", c.ref.label === undefined && "font-mono")}>{name}</span>
          <small className="block text-xs text-muted">
            {c.txCount} {c.txCount === 1 ? "tx" : "txs"} · net {netText(c.net)}
          </small>
          <span className="mt-1 block h-[3px] overflow-hidden rounded-full bg-surface-3" aria-hidden="true">
            <i className="block h-full rounded-full bg-muted" style={{ width: `${percent}%` }} />
          </span>
        </span>
      </button>
    </li>
  );
}

export function CounterpartiesCard({ state, onRetry, selected, onToggle }: CounterpartiesCardProps) {
  return (
    <Card
      title={
        <>
          Who they deal with
          <Help tip={TIP} />
        </>
      }
    >
      <Query
        state={state}
        onRetry={onRetry}
        loading={
          <div className="space-y-1">
            {Array.from({ length: COUNTERPARTY_LIMIT }, (_, i) => (
              <Skeleton key={i} className={ROW_SKELETON_CLASS} />
            ))}
          </div>
        }
      >
        {(list) =>
          list.length === 0 ? (
            <p className="py-4 text-sm text-muted">No counterparties yet. This wallet has not sent to or received from other wallets.</p>
          ) : (
            <ul className="space-y-0.5">
              {list.map((c, i) => (
                <CounterpartyRow
                  key={c.ref.address}
                  c={c}
                  percent={barPercents(list)[i] as number}
                  pressed={selected?.address === c.ref.address}
                  onToggle={() => onToggle(c.ref)}
                />
              ))}
            </ul>
          )
        }
      </Query>
    </Card>
  );
}
