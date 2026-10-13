import { useParams } from "react-router";
import { useQuery } from "../data/use-query";
import { parseDenom } from "../model/lookup";
import type { TokenDetail } from "../model/tokens";
import { NotFoundBox, Query, Skeleton } from "../ui/query-states";
import { Page } from "../ui/page";
import { useDocumentTitle } from "../ui/use-document-title";
import { useKeyPaged } from "../ui/use-key-paged";
import { FrozenCheck } from "./token/frozen-check";
import { HoldersCard } from "./token/holders-card";
import { PowersCard, TokenFacts, TokenHeader } from "./token/token-summary";

const NOT_FOUND_TITLE = "Token not found";

function TokenView({ token }: { token: TokenDetail }) {
  const holders = useKeyPaged((s, q) => s.assets.getTokenHolders(token.denom, q), [token.denom]);
  return (
    <>
      <TokenHeader token={token} />
      <div className="grid gap-4 lg:grid-cols-2">
        <TokenFacts token={token} />
        <PowersCard token={token} />
      </div>
      <HoldersCard paged={holders} />
      <FrozenCheck denom={token.denom} />
    </>
  );
}

function TokenLoader({ denom }: { denom: string }) {
  const { state, refetch } = useQuery((s) => s.assets.getToken(denom), [denom]);
  useDocumentTitle(state.status === "ready" && state.data ? state.data.name : "Token");
  return (
    <Query state={state} subject="this token" onRetry={refetch} loading={<Skeleton className="h-80 w-full" />}>
      {(token) =>
        token === null ? (
          <NotFoundBox title={NOT_FOUND_TITLE} hint="x/token has no token with this denom. It may be a typo, or the token may be on a different chain." />
        ) : (
          <TokenView token={token} />
        )
      }
    </Query>
  );
}

/** `/explorer/token/:creator/:subdenom` */
export function TokenPage() {
  const { creator, subdenom } = useParams();
  const denom = parseDenom(`${creator ?? ""}/${subdenom ?? ""}`);
  useDocumentTitle(denom === null ? NOT_FOUND_TITLE : null);
  return (
    <Page>
      {denom === null ? (
        <NotFoundBox title={NOT_FOUND_TITLE} hint="A token page is the creator's orama1… address and the token's lowercase name, such as /token/orama1…/cash." />
      ) : (
        <TokenLoader key={denom} denom={denom} />
      )}
    </Page>
  );
}
