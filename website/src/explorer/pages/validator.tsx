import { Link, useParams } from "react-router";
import { useQuery } from "../data/use-query";
import { useExplorerPaths } from "../network/active";
import type { ValidatorDetail } from "../model/types";
import { shortAddress } from "../model/units";
import { Card } from "../ui/card";
import { Page } from "../ui/page";
import { NotFoundBox, Query, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { walletParam } from "./wallet/wallet-param";
import { ValidatorHeader } from "./validator/validator-header";
import { ValidatorHistoryView } from "./validator/validator-history";
import { ValidatorRewardsCard } from "./rewards/validator-rewards";
import { ValidatorOperator } from "./validator/validator-operator";
import { ValidatorStats } from "./validator/validator-stats";

const NOT_FOUND_TITLE = "Validator not found";
const NOT_AN_OPERATOR_HINT =
  "That does not look like an Orama wallet address. A validator's page is its operator's wallet address, which starts with orama1.";

/** What the chain does not give, said once, so a missing figure is not read as zero. */
function NotExposed() {
  return (
    <Card title="Not shown">
      <p className="max-w-[75ch] text-sm text-muted">
        The chain does not expose how many delegators a validator has, so that is not shown. What a validator was paid is shown for the epochs
        in which the chain reported it; an earlier epoch is marked not recorded.
      </p>
    </Card>
  );
}

function ValidatorView({ detail }: { detail: ValidatorDetail }) {
  const address = detail.validator.ref.operator;
  const history = useQuery((s) => s.getValidatorHistory(address), [address]);
  return (
    <>
      <ValidatorHeader detail={detail} />
      <ValidatorStats detail={detail} />
      <ValidatorHistoryView state={history.state} onRetry={history.refetch} />
      <ValidatorRewardsCard address={address} />
      <ValidatorOperator detail={detail} />
      <NotExposed />
    </>
  );
}

/** Loads one validator. Keyed by address by its caller, so it never shows another validator's page. */
function ValidatorLoader({ address }: { address: string }) {
  const paths = useExplorerPaths();
  const { state, refetch } = useQuery((s) => s.getValidator(address), [address]);
  useDocumentTitle(state.status === "ready" && state.data ? state.data.validator.ref.moniker : `Validator ${shortAddress(address)}`);
  return (
    <Query state={state} subject="this validator" onRetry={refetch} loading={<Skeleton className="h-80 w-full" />}>
      {(detail) =>
        detail === null ? (
          <>
            <NotFoundBox title={NOT_FOUND_TITLE} hint="No validator has this operator wallet. It may be a wallet that has never run one." />
            <p className="text-center text-sm">
              <Link to={paths.wallet(address)} className="underline underline-offset-4 hover:text-fg">
                Open it as a wallet
              </Link>
            </p>
          </>
        ) : (
          <ValidatorView detail={detail} />
        )
      }
    </Query>
  );
}

/** `/explorer/validator/:address`, where the address is the validator's operator wallet. */
export function ValidatorPage() {
  const address = walletParam(useParams().address);
  useDocumentTitle(address === null ? NOT_FOUND_TITLE : null);
  return (
    <Page>
      {address === null ? <NotFoundBox title={NOT_FOUND_TITLE} hint={NOT_AN_OPERATOR_HINT} /> : <ValidatorLoader key={address} address={address} />}
    </Page>
  );
}
