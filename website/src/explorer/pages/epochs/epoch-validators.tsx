import { useSearchParams } from "react-router";
import { useQuery } from "../../data/use-query";
import type { EpochValidator, IndexPage } from "../../model/epochs";
import { formatInt, formatNorama } from "../../model/units";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { IndexQuery } from "../../ui/index-query";
import { ValidatorLink } from "../../ui/links";
import { Skeleton } from "../../ui/query-states";
import { parsePageParam, uptimePercent } from "./logic";
import { Pager } from "./pager";

const VALIDATORS_PER_PAGE = 25;
export const VALIDATOR_PAGE_PARAM = "vpage";
const TH = "px-3 py-2 font-medium";
const TD = "px-3 py-2";

const TIP_UPTIME = "Blocks the validator signed, out of the blocks it was asked to sign (signed plus missed) in this epoch.";

function statusBadge(v: EpochValidator) {
  if (v.jailed) return <Badge tone="bad">jailed</Badge>;
  return <Badge tone={v.status === "bonded" ? "ok" : "mute"}>{v.status}</Badge>;
}

function Table({ data }: { data: IndexPage<EpochValidator> }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[620px] text-left text-sm">
        <caption className="sr-only">Validators of this epoch, highest voting power first</caption>
        <thead className="text-xs text-muted">
          <tr>
            <th scope="col" className={TH}>Validator</th>
            <th scope="col" className={`${TH} text-right`}>Power</th>
            <th scope="col" className={`${TH} text-right`}>
              Uptime
              <Help tip={TIP_UPTIME} />
            </th>
            <th scope="col" className={`${TH} text-right`}>Signed / missed</th>
            <th scope="col" className={`${TH} text-right`}>Slashes</th>
            <th scope="col" className={TH}>Status</th>
          </tr>
        </thead>
        <tbody>
          {data.items.map((v) => (
            <tr key={v.ref.operator} className="border-t border-border">
              <td className={TD}>
                <ValidatorLink validator={v.ref} />
              </td>
              <td className={`${TD} text-right font-mono tabular-nums`}>{formatInt(v.power)}</td>
              <td className={`${TD} text-right font-mono tabular-nums`}>{uptimePercent(v.uptimeBps)}</td>
              <td className={`${TD} text-right font-mono tabular-nums`}>
                {formatInt(v.signedBlocks)} / {formatInt(v.missedBlocks)}
              </td>
              <td className={`${TD} text-right font-mono tabular-nums`} title={v.slashes > 0 ? `${formatNorama(v.slashedBurned)} ORAMA burned` : undefined}>
                {formatInt(v.slashes)}
              </td>
              <td className={TD}>{statusBadge(v)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Body({ data, page }: { data: IndexPage<EpochValidator>; page: number }) {
  if (data.items.length === 0) {
    return (
      <p className="py-4 text-center text-sm text-muted">
        {page === 1 ? "The index holds no validator for this epoch." : "There are no more validators for this epoch."}
      </p>
    );
  }
  return (
    <>
      <Table data={data} />
      <Pager param={VALIDATOR_PAGE_PARAM} page={page} hasMore={data.hasMore} />
    </>
  );
}

/** The validators of one closed epoch. Its page lives in the URL as ?vpage=. */
export function EpochValidators({ epoch }: { epoch: number }) {
  const [params] = useSearchParams();
  const page = parsePageParam(params.get(VALIDATOR_PAGE_PARAM));
  const { state, refetch } = useQuery((s) => s.getEpochValidators(epoch, { page, limit: VALIDATORS_PER_PAGE }), [epoch, page]);
  return (
    <Card title="Validators in this epoch">
      <IndexQuery state={state} onRetry={refetch} loading={<Skeleton className="h-40 w-full" />}>
        {(data) => <Body data={data ?? { items: [], page, limit: VALIDATORS_PER_PAGE, hasMore: false }} page={page} />}
      </IndexQuery>
    </Card>
  );
}
