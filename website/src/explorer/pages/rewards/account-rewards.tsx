import { Link } from "react-router";
import { useQuery } from "../../data/use-query";
import type { QueryState } from "../../data/use-query";
import type { AccountReward, AccountRewards, Earnings } from "../../model/rewards";
import { useExplorerPaths } from "../../network/active";
import { formatInt } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { IndexQuery } from "../../ui/index-query";
import { ValidatorLink } from "../../ui/links";
import { LoadMore } from "../../ui/load-more";
import { Query, Skeleton } from "../../ui/query-states";
import { useKeyPaged } from "../../ui/use-key-paged";
import type { KeyPaged } from "../../ui/use-key-paged";
import { recordedFrom } from "./logic";

const REWARDS_SHOWN = 20;
const AMOUNT_FRACTION = 4;

const TIP_EARNINGS =
  "Every protocol payout (validator rewards, storage and relay payments, fees) is credited to the account's earnings first. The account moves it to its public balance with a withdraw transaction. It is not in the balance shown above.";

export const NO_REWARDS = "No staking reward has been credited to this account in the epochs the index recorded.";

/** The balance of the account's earnings, read from the chain. */
export function EarningsView({ state, onRetry }: { state: QueryState<Earnings>; onRetry: () => void }) {
  return (
    <Card
      title={
        <>
          Earnings
          <Help tip={TIP_EARNINGS} />
        </>
      }
    >
      <Query state={state} onRetry={onRetry} loading={<Skeleton className="h-10 w-48" />}>
        {(earnings) => (
          <div className="flex flex-wrap items-baseline gap-x-2.5">
            <span className="text-[26px] font-semibold leading-tight tracking-tight">
              <Amount norama={earnings.balance} bare maxFraction={AMOUNT_FRACTION} className="font-sans" />
            </span>
            <span className="text-sm text-muted">ORAMA waiting to be withdrawn</span>
          </div>
        )}
      </Query>
    </Card>
  );
}

export function EarningsCard({ address }: { address: string }) {
  const { state, refetch } = useQuery((s) => s.getEarnings(address), [address]);
  return <EarningsView state={state} onRetry={refetch} />;
}

function Rows({ items }: { items: AccountReward[] }) {
  const paths = useExplorerPaths();
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-left text-sm">
        <caption className="sr-only">Staking rewards credited to this account, newest epoch first</caption>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-muted">
            <th scope="col" className="pb-2 pr-3 font-medium">Epoch</th>
            <th scope="col" className="pb-2 pr-3 font-medium">From</th>
            <th scope="col" className="pb-2 pr-3 text-right font-medium">Credited (ORAMA)</th>
          </tr>
        </thead>
        <tbody>
          {items.map((r) => (
            <tr key={`${r.epoch}-${r.validator.operator}`} className="border-t border-border/60">
              <th scope="row" className="py-2 pr-3 text-left font-mono font-normal tabular-nums">
                <Link to={paths.epoch(r.epoch)} className="underline decoration-dotted underline-offset-4">
                  {formatInt(r.epoch)}
                </Link>
              </th>
              <td className="py-2 pr-3">
                <ValidatorLink validator={r.validator} />
                {r.role === "operator" && <Badge tone="mute" className="ml-2">its operator</Badge>}
              </td>
              <td className="py-2 pr-3 text-right">
                <Amount norama={r.amount} bare maxFraction={AMOUNT_FRACTION} />
                {BigInt(r.forceBonded) > 0n && <span className="ml-1.5 text-xs text-muted">(<Amount norama={r.forceBonded} bare maxFraction={AMOUNT_FRACTION} /> staked)</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** The ready state: the rows loaded so far, or that there are none and from which epoch that is known. */
export function AccountRewardsBody({ paged, recordedFromEpoch }: { paged: KeyPaged<AccountReward, AccountRewards>; recordedFromEpoch: number }) {
  return (
    <>
      {paged.items.length === 0 ? <p className="text-sm text-muted">{NO_REWARDS}</p> : <Rows items={paged.items} />}
      <LoadMore paged={paged} what="rewards" />
      <p className="mt-3 text-[13px] text-muted">{recordedFrom(recordedFromEpoch)}</p>
    </>
  );
}

export function AccountRewardsView({ paged }: { paged: KeyPaged<AccountReward, AccountRewards> }) {
  return (
    <Card title="Staking rewards">
      <IndexQuery state={paged.first} onRetry={paged.retry} loading={<Skeleton className="h-32 w-full" />}>
        {(first) => <AccountRewardsBody paged={paged} recordedFromEpoch={first.recordedFromEpoch} />}
      </IndexQuery>
    </Card>
  );
}

/**
 * What the validators it stakes with (or runs) credited to this account, newest epoch first. More
 * are read from the indexer's cursor on request, so a deep page costs what the first does.
 */
export function AccountRewardsCard({ address }: { address: string }) {
  const paged = useKeyPaged((s, q) => s.getAccountRewards(address, q), [address], REWARDS_SHOWN);
  return <AccountRewardsView paged={paged} />;
}
