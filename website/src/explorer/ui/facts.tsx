import type { ReactNode } from "react";

const ROW_GRID = "grid grid-cols-[110px_minmax(0,1fr)] gap-x-4 gap-y-2.5 text-[13px] sm:grid-cols-[170px_minmax(0,1fr)]";

/** Labelled facts about one thing, one row each. A row with no value (undefined) is left out. */
export function Facts({ rows }: { rows: readonly (readonly [string, ReactNode | undefined])[] }) {
  return (
    <dl className={ROW_GRID}>
      {rows.map(([term, value]) =>
        value === undefined ? null : (
          <div key={term} className="contents">
            <dt className="text-muted">{term}</dt>
            <dd className="m-0 [overflow-wrap:anywhere]">{value}</dd>
          </div>
        ),
      )}
    </dl>
  );
}

/** Text the chain does not carry, said in the place the value would be. */
export function Absent({ children }: { children: ReactNode }) {
  return <span className="text-muted">{children}</span>;
}
