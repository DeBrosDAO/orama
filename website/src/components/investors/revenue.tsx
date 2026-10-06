import {
  ORAMA_PRICING,
  ORAMA_REVENUE,
  ROOTWALLET_REVENUE,
  WALLET_BASE_ASSUMPTIONS,
  WALLET_BENCHMARKS,
  WALLET_SCENARIOS,
  walletRevenuePerUser,
} from "../../content/investors/model";
import {
  HORIZON_MONTH,
  ORAMA_INPUTS,
  PROJECTION_ASSUMPTIONS,
  SCENARIO_IDS,
  formatApproxEur,
  oramaArrEur,
  walletArrEur,
} from "../../content/investors/projections";
import { FUNDING_MONTHS, WALLET_USER_TARGET } from "../../content/funding";
import { cn } from "../../lib/utils";
import { CardList } from "./blocks";
import { SourceRef } from "./source-ref";

const USD2 = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2 });

function IllustrativeTag() {
  return <span className="font-mono text-[10px] tracking-wider uppercase text-signal">Illustrative, not a forecast</span>;
}

function Assumptions({ items }: { items: readonly string[] }) {
  return (
    <ul className="mt-4 flex flex-col gap-1 text-xs text-muted">
      {items.map((a) => (
        <li key={a}>· {a}</li>
      ))}
    </ul>
  );
}

export function OramaRevenue() {
  return (
    <div className="flex flex-col gap-6">
      <CardList columns={2} items={ORAMA_REVENUE.map((r) => ({ title: r.title, line: r.line, tag: r.when }))} />
      <div className="p-6 border border-fg/20 bg-fg/[0.02] flex flex-col gap-2">
        <p className="font-display font-semibold text-xl text-fg">
          {ORAMA_PRICING.headline}
          <SourceRef ids={ORAMA_PRICING.sources} />
        </p>
        <p className="text-sm text-accent">{ORAMA_PRICING.line}</p>
        <p className="font-mono text-[10px] tracking-wider uppercase text-muted">{ORAMA_PRICING.note}</p>
      </div>
    </div>
  );
}

export function WalletRevenue() {
  return (
    <div className="flex flex-col gap-6">
      <CardList columns={2} items={ROOTWALLET_REVENUE.map((r) => ({ title: r.title, line: r.line, tag: r.when }))} />
      <div className="p-6 border border-dashed border-border">
        <h3 className="font-display font-semibold text-fg mb-4">What wallets earn per monthly user, per year</h3>
        <ul className="grid grid-cols-1 sm:grid-cols-3 gap-px bg-border border border-border">
          {WALLET_BENCHMARKS.map((b) => (
            <li key={b.name} className="flex flex-col gap-1 p-4 bg-surface">
              <span className="font-mono text-[10px] tracking-wider uppercase text-muted">{b.name}</span>
              <span className="font-display font-bold text-2xl text-fg">
                {b.value}
                <SourceRef ids={b.sources} />
              </span>
              <span className="text-xs text-muted">{b.basis}</span>
            </li>
          ))}
        </ul>
        <p className="mt-4 text-sm text-accent">
          RootWallet, fees only, per monthly user per year:{" "}
          {SCENARIO_IDS.map((id) => USD2.format(walletRevenuePerUser(WALLET_SCENARIOS[id]))).join(" / ")}{" "}
          <span className="text-muted">(conservative / base / upside)</span>
        </p>
        <Assumptions items={WALLET_BASE_ASSUMPTIONS} />
      </div>
    </div>
  );
}

interface Row {
  label: string;
  values: number[];
  strong?: boolean;
}

function projectionRows(): Row[] {
  const orama = SCENARIO_IDS.map((id) => oramaArrEur(ORAMA_INPUTS[id], FUNDING_MONTHS));
  const wallet = SCENARIO_IDS.map((id) => walletArrEur(id));
  return [
    { label: `Orama, month ${FUNDING_MONTHS}`, values: orama },
    { label: `RootWallet fees at ${WALLET_USER_TARGET.toLocaleString("en-US")} users`, values: wallet },
    { label: `Together, month ${FUNDING_MONTHS}`, values: orama.map((v, i) => v + wallet[i]), strong: true },
    { label: "Orama, three years after funding", values: SCENARIO_IDS.map((id) => oramaArrEur(ORAMA_INPUTS[id], HORIZON_MONTH)) },
  ];
}

/** Yearly revenue run-rate when the next round is raised, and in year three. */
export function Projections() {
  return (
    <div className="p-6 border border-dashed border-border">
      <div className="flex flex-wrap items-baseline justify-between gap-2 mb-4">
        <p className="text-sm text-muted">Yearly revenue run-rate, in euros</p>
        <IllustrativeTag />
      </div>
      <div className="overflow-x-auto" tabIndex={0} role="region" aria-label="Revenue illustrations">
        <table className="w-full min-w-[520px] text-sm">
          <thead>
            <tr className="border-b border-dashed border-border">
              <th scope="col" className="sticky left-0 z-10 bg-surface p-3 text-left font-normal">
                <span className="sr-only">Line</span>
              </th>
              {SCENARIO_IDS.map((id) => (
                <th key={id} scope="col" className="p-3 text-right font-mono text-[10px] tracking-wider uppercase text-muted font-normal">
                  {id}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {projectionRows().map((row) => (
              <tr key={row.label} className={cn("border-b border-dashed border-border last:border-0", row.strong && "bg-fg/[0.03]")}>
                <th scope="row" className={cn("sticky left-0 z-10 bg-surface p-3 text-left font-normal", row.strong ? "text-fg font-semibold" : "text-accent")}>
                  {row.label}
                </th>
                {row.values.map((v, i) => (
                  <td key={SCENARIO_IDS[i]} className={cn("p-3 text-right font-display tabular-nums text-fg", row.strong && "text-lg font-bold")}>
                    {formatApproxEur(v)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Assumptions items={PROJECTION_ASSUMPTIONS} />
      <p className="mt-2 text-xs text-muted">
        Benchmarks and currency conversion
        <SourceRef ids={["supabase-arr", "fx"]} />
      </p>
    </div>
  );
}
