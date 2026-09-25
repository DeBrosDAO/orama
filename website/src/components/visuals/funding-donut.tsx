import {
  ALLOCATIONS,
  FUNDING_MONTHS,
  FUNDING_TOTAL_EUR,
  formatEur,
  formatEurShort,
} from "../../content/funding";

const RADIUS = 80;
const STROKE = 26;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;
/** Hairline gap between segments, in px of arc. */
const GAP = 3;

/**
 * Monochrome ramp, strongest for the largest slice. The colours are CSS
 * variables (index.css) so the printed page can flip the ramp for paper.
 */
export const SHADE_COUNT = 7;
const shade = (i: number) => `var(--funding-shade-${i % SHADE_COUNT})`;

export function FundingDonut() {
  let offset = 0;
  const segments = ALLOCATIONS.map((a, i) => {
    const len = (a.amountEur / FUNDING_TOTAL_EUR) * CIRCUMFERENCE;
    const seg = { ...a, len, offset, shade: shade(i) };
    offset += len;
    return seg;
  });

  return (
    <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,320px)_1fr] gap-10 items-center">
      <div className="relative mx-auto w-full max-w-[320px] aspect-square">
        <svg viewBox="0 0 200 200" className="w-full h-full -rotate-90" role="img" aria-label="Use of funds">
          <circle cx={100} cy={100} r={RADIUS} fill="none" className="stroke-border/40" strokeWidth={STROKE} />
          {segments.map((s) => (
            <circle
              key={s.id}
              cx={100}
              cy={100}
              r={RADIUS}
              fill="none"
              style={{ stroke: s.shade }}
              strokeWidth={STROKE}
              strokeDasharray={`${Math.max(s.len - GAP, 0)} ${CIRCUMFERENCE}`}
              strokeDashoffset={-s.offset}
            />
          ))}
        </svg>
        <div className="absolute inset-0 flex flex-col items-center justify-center">
          <span className="font-display font-bold text-4xl text-fg">
            {formatEurShort(FUNDING_TOTAL_EUR)}
          </span>
          <span className="font-mono text-[11px] tracking-wider uppercase text-muted">{FUNDING_MONTHS} months</span>
        </div>
      </div>

      <ul className="flex flex-col divide-y divide-dashed divide-border border-y border-dashed border-border">
        {segments.map((s) => (
          <li key={s.id} className="flex items-center gap-4 py-3">
            <span className="w-3 h-3 rounded-sm shrink-0" style={{ background: s.shade }} />
            <span className="flex-1 min-w-0">
              <span className="block text-sm text-fg">{s.label}</span>
              <span className="block text-xs text-muted">{s.delivers}</span>
            </span>
            <span className="font-mono text-sm text-fg tabular-nums" title={formatEur(s.amountEur)}>
              {formatEurShort(s.amountEur)}
            </span>
            <span className="font-mono text-xs text-muted tabular-nums w-10 text-right">
              {Math.round((s.amountEur / FUNDING_TOTAL_EUR) * 100)}%
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
