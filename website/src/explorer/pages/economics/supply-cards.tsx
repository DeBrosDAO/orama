import type { SupplyPoint } from "../../model/epochs";
import { formatDate, formatUtc } from "../../model/time";
import { formatInt } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { BlockLink } from "../../ui/links";
import { Stat } from "../../ui/stat";
import { composition, permilleText } from "./model";
import type { SliceKey } from "./model";

const TIP: Record<SliceKey, string> = {
  circulating: "Supply that no protocol module account holds: in wallets, free to move.",
  bonded: "ORAMA delegated to validators and locked in the staking pool to secure the chain.",
  unbonding: "ORAMA on its way out of staking, held until the unbonding period ends.",
  earningsPools: "Rewards and fees credited to earners that they have not withdrawn yet. x/fees holds them in its account.",
  storageEscrow: "ORAMA that storage clients paid up front for deals, held until the providers earn it.",
  otherProtocol: "What the other protocol module accounts hold: the fee collector, distribution, emission, power, fee deposits, token, archive, nodes, houses and storage module accounts. Nobody holds this as a wallet balance.",
};

const SWATCH: Record<SliceKey, string> = {
  circulating: "bg-gain",
  bonded: "bg-signal",
  unbonding: "bg-signal/50",
  earningsPools: "bg-accent-2",
  storageEscrow: "bg-accent",
  otherProtocol: "bg-muted",
};

/** The figures the supply tiles and the breakdown are read at: after epoch N's closing block. */
export function AsOf({ point }: { point: SupplyPoint }) {
  return (
    <p className="text-sm text-muted">
      As of the end of epoch {formatInt(point.epoch)} (block <BlockLink height={point.height} />,{" "}
      <time dateTime={point.time} title={formatUtc(point.time)}>
        {formatDate(point.time)}
      </time>
      ). Figures move once per epoch.
    </p>
  );
}

export function SupplyTiles({ point }: { point: SupplyPoint }) {
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <Stat label="Total supply" value={<Amount norama={point.totalSupply} bare />} detail="ORAMA that exists" />
      <Stat label="Circulating" value={<Amount norama={point.circulating} bare />} detail="ORAMA outside protocol accounts" />
      <Stat
        label="Minted to date"
        value={<Amount norama={point.mintedToDate} bare />}
        detail={
          <>
            created by the chain since it began, on top of <Amount norama={point.genesisSupply} bare /> at genesis
          </>
        }
      />
      <Stat label="Burned to date" value={<Amount norama={point.burnedToDate} bare />} detail="ORAMA the chain has destroyed" />
    </div>
  );
}

export function CompositionCard({ point }: { point: SupplyPoint }) {
  const slices = composition(point);
  return (
    <Card title="Where the supply sits">
      <div
        role="img"
        aria-label={`Share of total supply: ${slices.map((s) => `${s.label} ${permilleText(s.permille)}`).join(", ")}`}
        className="flex h-3 overflow-hidden rounded-full bg-surface-3"
      >
        {slices
          .filter((s) => s.permille > 0)
          .map((s) => (
            <span key={s.key} className={SWATCH[s.key]} style={{ width: `${s.permille / 10}%` }} />
          ))}
      </div>
      <div className="mt-4 overflow-x-auto">
        <table className="w-full min-w-[420px] text-left text-sm">
          <caption className="sr-only">Total supply by holder, in ORAMA</caption>
          <thead className="text-xs text-muted">
            <tr>
              <th scope="col" className="px-3 py-2 font-medium">Held as</th>
              <th scope="col" className="px-3 py-2 text-right font-medium">ORAMA</th>
              <th scope="col" className="px-3 py-2 text-right font-medium">Share</th>
            </tr>
          </thead>
          <tbody>
            {slices.map((s) => (
              <tr key={s.key} className="border-t border-border">
                <th scope="row" className="px-3 py-2 font-normal">
                  <span className={`mr-2 inline-block h-2.5 w-2.5 rounded-sm align-middle ${SWATCH[s.key]}`} aria-hidden="true" />
                  {s.label}
                  <Help tip={TIP[s.key]} />
                </th>
                <td className="px-3 py-2 text-right">
                  <Amount norama={s.amount.toString()} bare />
                </td>
                <td className="px-3 py-2 text-right font-mono tabular-nums text-muted">{permilleText(s.permille)}</td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr className="border-t border-border font-medium">
              <th scope="row" className="px-3 py-2 text-left">Total supply</th>
              <td className="px-3 py-2 text-right">
                <Amount norama={point.totalSupply} bare />
              </td>
              <td className="px-3 py-2 text-right font-mono tabular-nums text-muted">100.0%</td>
            </tr>
          </tfoot>
        </table>
      </div>
    </Card>
  );
}
