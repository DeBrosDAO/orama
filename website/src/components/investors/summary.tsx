import { Mail } from "lucide-react";
import { FUNDING_MONTHS, FUNDING_TOTAL_EUR, MONTHLY_BURN_EUR, PAID_BETA_MONTH, WALLET_USER_TARGET, formatEur, formatEurShort } from "../../content/funding";
import { COMPANY, SHIPPED } from "../../content/investors/products";
import { DISCLAIMER } from "../../content/investors/case";
import { SERVICES } from "../../content/services";
import { Button } from "../ui/button";

/** The page in 30 seconds, for investors who read nothing else. */
export function ThirtySeconds() {
  const cells = [
    { k: "The ask", v: `${formatEurShort(FUNDING_TOTAL_EUR)} equity, no token. ${COMPANY.form}. ${FUNDING_MONTHS} months of runway.` },
    { k: "What's built", v: `Orama Network: ${SERVICES.length} live cloud services running an independent app's whole backend. RootWallet: the wallet and vault that signs you in.` },
    { k: "Revenue starts", v: `Orama's paid beta at month ${PAID_BETA_MONTH}. RootWallet's public launch in Q1 2027.` },
    { k: `By month ${FUNDING_MONTHS}`, v: `Paying teams, measured uptime, OramaOS on real hardware, ${WALLET_USER_TARGET.toLocaleString("en-US")} wallet users. Then the next round.` },
  ];
  return (
    <section aria-labelledby="thirty-seconds" className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
      <div className="border border-fg/20 bg-white/[0.02]">
        <h2 id="thirty-seconds" className="px-5 pt-5 font-mono text-[11px] tracking-[0.25em] uppercase text-muted">
          The 30-second version
        </h2>
        <dl className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4">
          {cells.map((c) => (
            <div key={c.k} className="flex flex-col gap-2 p-5">
              <dt className="font-display font-semibold text-fg">{c.k}</dt>
              <dd className="text-sm text-accent">{c.v}</dd>
            </div>
          ))}
        </dl>
      </div>
    </section>
  );
}

/** "Who are you?", answered with what the team built. */
export function Shipped() {
  const facts = [
    { value: __REPO_COMMITS__.toLocaleString("en-US"), label: "commits to Orama since August 2025" },
    ...SHIPPED.map((s) => ({ value: "derived" in s ? String(SERVICES.length) : s.value, label: s.label })),
  ];
  return (
    <ul className="grid grid-cols-2 lg:grid-cols-5 gap-px bg-border border border-border">
      {facts.map((f) => (
        <li key={f.label} className="flex flex-col gap-1 p-5 bg-surface">
          <span className="font-display font-bold text-3xl text-fg tabular-nums">{f.value}</span>
          <span className="text-xs text-muted">{f.label}</span>
        </li>
      ))}
    </ul>
  );
}

export function RoundTerms({ mailto, email }: { mailto: string; email: string }) {
  const rows = [
    ["Amount", formatEur(FUNDING_TOTAL_EUR)],
    ["Instrument", "Equity. No token, no loans"],
    ["Company", COMPANY.form],
    ["Runway", `${FUNDING_MONTHS} months, about ${formatEurShort(MONTHLY_BURN_EUR)} a month`],
    ["Team", `${COMPANY.team} today; the round pays ${COMPANY.hires}`],
    ["Valuation", "Shared directly with investors"],
  ];
  return (
    <div className="flex flex-col gap-4">
      <dl className="grid grid-cols-1 sm:grid-cols-2 gap-px bg-border border border-border">
        {rows.map(([k, v]) => (
          <div key={k} className="flex flex-col gap-1 p-5 bg-surface">
            <dt className="font-mono text-[10px] tracking-wider uppercase text-muted">{k}</dt>
            <dd className="text-fg">{v}</dd>
          </div>
        ))}
      </dl>
      <p className="text-xs text-muted max-w-3xl">{DISCLAIMER}</p>
      <div>
        <Button asChild size="sm" variant="ghost" className="rounded-full">
          <a href={mailto}>
            <Mail className="w-3 h-3 mr-2" />
            {email}
          </a>
        </Button>
      </div>
    </div>
  );
}
