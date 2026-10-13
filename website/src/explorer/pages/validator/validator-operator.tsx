import { Link } from "react-router";
import { useQuery } from "../../data/use-query";
import { useExplorerPaths } from "../../network/active";
import type { OperatorGroups, PowerOperator, PowerCap, ValidatorDetail } from "../../model/types";
import { formatPct } from "../../model/units";
import { Card } from "../../ui/card";
import { Query, Skeleton } from "../../ui/query-states";
import { WalletLink } from "../../ui/links";
import { capSentence } from "../validators/operators-card";

function OperatorName({ operator }: { operator: PowerOperator }) {
  return operator.kind === "registered" ? (
    <WalletLink wallet={{ address: operator.address }} />
  ) : (
    <b title="Its consensus key is not bound to a registered node (x/nodes)">no registered node</b>
  );
}

function Siblings({ groups, detail }: { groups: OperatorGroups; detail: ValidatorDetail }) {
  const paths = useExplorerPaths();
  const me = detail.validator.ref.operator;
  const group = groups.groups.find((g) => g.validators.some((v) => v.ref.operator === me));
  const others = group?.validators.filter((v) => v.ref.operator !== me) ?? [];
  if (group === undefined) {
    return <p className="text-sm text-muted">This validator is not in the active set, so it is in no group.</p>;
  }
  return (
    <div className="space-y-1 text-sm">
      <p>
        This operator&apos;s validators hold <b>{formatPct(group.power)}</b> of the voting power together
        {others.length === 0 ? ", and this is the only one." : "."}
      </p>
      {others.length > 0 && (
        <p>
          The others:{" "}
          {others.map((v, i) => (
            <span key={v.ref.operator}>
              {i > 0 && ", "}
              <Link to={paths.validator(v.ref.operator)} className="underline decoration-dotted underline-offset-4 hover:text-fg">
                {v.ref.moniker}
              </Link>
            </span>
          ))}
          .
        </p>
      )}
      {groups.unresolved > 0 && (
        <p className="text-[13px] text-muted">Only the largest validators are grouped; smaller ones may share this operator without showing here.</p>
      )}
    </div>
  );
}

function Body({ detail, cap }: { detail: ValidatorDetail; cap: PowerCap }) {
  const groups = useQuery((s) => s.getOperatorGroups(), []);
  const power = detail.power;
  if (power === null) return <p className="text-sm text-muted">x/power does not list this validator, so it has no operator.</p>;
  return (
    <div className="space-y-3">
      <p className="text-sm">
        Counted under the operator <OperatorName operator={power.operator} />.
      </p>
      <p className="max-w-[75ch] text-[13px] text-muted">{capSentence(cap)}</p>
      <Query state={groups.state} onRetry={groups.refetch} loading={<Skeleton className="h-10 w-full" />}>
        {(g) => <Siblings groups={g} detail={detail} />}
      </Query>
    </div>
  );
}

/** Which operator the validator's stake counts under for x/power's cap, and who else that operator runs. */
export function ValidatorOperator({ detail }: { detail: ValidatorDetail }) {
  const set = useQuery((s) => s.getValidators(), []);
  return (
    <Card title="Operator and the power cap">
      <Query state={set.state} onRetry={set.refetch} loading={<Skeleton className="h-16 w-full" />}>
        {(s) => <Body detail={detail} cap={s.handover.cap} />}
      </Query>
    </Card>
  );
}
