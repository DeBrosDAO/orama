import type { ValidatorDetail } from "../../model/types";
import { formatDate } from "../../model/time";
import { formatCompact, formatInt, formatNorama, formatPct } from "../../model/units";
import { Help } from "../../ui/help";
import { Stat } from "../../ui/stat";

const ORAMA = <span className="text-sm font-normal text-muted">ORAMA</span>;

const COMMISSION_TIP =
  "The share of its delegators' rewards the validator keeps. The maximum rate is the most it can ever charge, and the maximum change is how far it may move the rate in one day.";
const SELF_BOND_TIP =
  "ORAMA the operator's own wallet has bonded to this validator. It is what the operator stands to lose from a slash alongside its delegators.";

/** The four figures a delegator weighs first. Each is a chain figure; none is estimated. */
export function ValidatorStats({ detail }: { detail: ValidatorDetail }) {
  const { validator: v, commission: c } = detail;
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <Stat
        label="Voting power"
        value={formatPct(v.power)}
        detail={detail.power === null ? "x/power does not list it" : `${formatInt(Number(detail.power.cometPower))} CometBFT power`}
      />
      <Stat
        label={
          <>
            Commission
            <Help tip={COMMISSION_TIP} />
          </>
        }
        value={formatPct(c.rate)}
        detail={`max ${formatPct(c.maxRate)}, moves at most ${formatPct(c.maxChangePerDay)} a day${c.updatedAt === null ? "" : `, changed ${formatDate(c.updatedAt)}`}`}
      />
      <Stat
        label={
          <>
            Self-bond
            <Help tip={SELF_BOND_TIP} />
          </>
        }
        value={<>{formatCompact(detail.selfBond)} {ORAMA}</>}
        detail={`minimum ${formatNorama(detail.minSelfDelegation)} ORAMA`}
      />
      <Stat
        label="Total delegated"
        value={<>{formatCompact(v.tokens)} {ORAMA}</>}
        detail={`includes the self-bond; ${formatNorama(detail.delegatedByOthers)} ORAMA is from other delegators`}
      />
    </div>
  );
}
