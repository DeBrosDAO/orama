import { useParams } from "react-router";
import { useQuery } from "../data/use-query";
import type { TxDetail } from "../model/types";
import { shortHash } from "../model/units";
import { NotFoundBox, Query } from "../ui/query-states";
import { Page } from "../ui/page";
import { useDocumentTitle } from "../ui/use-document-title";
import { BalancesCard } from "./tx/tx-balances";
import { FlowCard } from "./tx/tx-flow";
import { TX_HASH_LENGTH, normalizeTxHash } from "./tx/tx-params";
import { InvestigateCard, NormalCard } from "./tx/tx-rail";
import { TxSkeleton } from "./tx/tx-skeleton";
import { TabsCard } from "./tx/tx-tabs";
import type { TabSpec } from "./tx/tx-tabs";
import { DetailsPanel, TechnicalPanel } from "./tx/tx-panels";
import { firstMessage } from "./tx/tx-messages";
import { TxVerdict } from "./tx/tx-verdict";

const TABS_ID = "tx-tabs";

function tabsFor(tx: TxDetail): TabSpec[] {
  return [
    { id: "details", label: "Details", panel: <DetailsPanel tx={tx} /> },
    {
      id: "technical",
      label: (
        <>
          Technical <span className="font-mono text-[11px] text-muted">(for developers)</span>
        </>
      ),
      panel: <TechnicalPanel tx={tx} />,
    },
  ];
}

function TxBody({ tx }: { tx: TxDetail }) {
  const message = firstMessage(tx.messages);
  return (
    <div className="space-y-4">
      <TxVerdict tx={tx} />
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        <div className="min-w-0 space-y-4">
          <FlowCard message={message} fee={tx.fee} failed={!tx.status.ok} />
          <BalancesCard changes={tx.balanceChanges} />
          <TabsCard tabs={tabsFor(tx)} label="Transaction information" idPrefix={TABS_ID} />
        </div>
        <div className="min-w-0 space-y-4 self-start">
          <InvestigateCard tx={tx} />
          <NormalCard tx={tx} />
        </div>
      </div>
    </div>
  );
}

function TxView({ hash }: { hash: string }) {
  const { state, refetch } = useQuery((s) => s.getTx(hash), [hash]);
  useDocumentTitle(`Transaction ${shortHash(hash)}`);
  return (
    <Query state={state} onRetry={refetch} loading={<TxSkeleton />}>
      {(tx) =>
        tx ? (
          <TxBody tx={tx} />
        ) : (
          <NotFoundBox
            title="Transaction not found"
            hint={`There is no transaction ${shortHash(hash)} on this chain. It may be a typo, or the transaction may be on a different chain.`}
          />
        )
      }
    </Query>
  );
}

/** `/explorer/tx/:hash` */
export function TxPage() {
  const { hash: raw } = useParams();
  const hash = normalizeTxHash(raw);
  return (
    <Page>
      {hash ? (
        <TxView key={hash} hash={hash} />
      ) : (
        <NotFoundBox
          title="Transaction not found"
          hint={`"${shortHash(raw ?? "")}" is not a transaction hash. A transaction hash is ${TX_HASH_LENGTH} characters: the digits 0-9 and the letters A-F.`}
        />
      )}
    </Page>
  );
}
