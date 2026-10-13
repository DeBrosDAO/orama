import type { RelayEpoch, TorRelays } from "../../model/tor";
import { formatInt, formatPct } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { WalletLink } from "../../ui/links";

function Settlement({ epoch, number }: { epoch: RelayEpoch | null; number: number }) {
  if (number < 1) return <p className="text-sm text-muted">No epoch is past its report window yet.</p>;
  if (epoch === null) {
    return (
      <p className="text-sm text-muted">
        The chain holds no relay settlement for epoch {formatInt(number)}, the newest one past its report window: nobody reported on it, or relay rewards are not on yet.
      </p>
    );
  }
  if (epoch.activating) {
    return <p className="text-sm">Epoch {formatInt(epoch.epoch)} switched relay rewards on. It minted nothing; the next settled epoch with enough reports pays.</p>;
  }
  return (
    <p className="text-sm">
      Epoch {formatInt(epoch.epoch)}: {epoch.quorumMet ? "enough reporters reported" : "too few reporters reported, so nothing was paid"}.{" "}
      <Amount norama={epoch.minted} maxFraction={2} /> minted of a <Amount norama={epoch.ceiling} maxFraction={2} /> ceiling.
    </p>
  );
}

/** How relays are rewarded: the rules x/relay holds, who reports, and what the newest closed epoch paid. */
export function RewardsCard({ tor }: { tor: TorRelays }) {
  const { params } = tor;
  return (
    <Card title="How relays are rewarded">
      <dl className="grid gap-x-8 gap-y-2 text-sm sm:grid-cols-[auto_1fr]">
        <dt className="text-muted">Reporters needed</dt>
        <dd>{params.reportersQuorum} distinct reporters must submit a complete report before an epoch pays</dd>
        <dt className="text-muted">Minimum uptime</dt>
        <dd>a relay under {formatPct(params.minUptime)} median uptime is paid nothing</dd>
        <dt className="text-muted">Exit multiplier</dt>
        <dd>{params.exitMultiplier}x on a relay the reporters measure as an exit</dd>
        <dt className="text-muted">Caps per epoch</dt>
        <dd>
          <Amount norama={params.perRelayCap} maxFraction={2} /> a relay, <Amount norama={params.perOperatorCap} maxFraction={2} /> an operator,{" "}
          <Amount norama={params.perPrefix16Cap} maxFraction={2} /> a /16 network
        </dd>
        <dt className="text-muted">Reporters ({tor.reporters.length})</dt>
        <dd>
          {tor.reporters.length === 0 ? (
            <span className="text-muted">none</span>
          ) : (
            <ul className="flex flex-wrap gap-x-4 gap-y-1">
              {tor.reporters.map((r) => (
                <li key={r}>
                  <WalletLink wallet={{ address: r }} bare />
                </li>
              ))}
            </ul>
          )}
        </dd>
      </dl>
      <h3 className="mt-4 text-[11px] font-semibold uppercase tracking-[0.09em] text-muted">Newest settled epoch</h3>
      <div className="mt-1">
        <Settlement epoch={tor.lastSettlement} number={tor.settlementEpoch} />
      </div>
    </Card>
  );
}
