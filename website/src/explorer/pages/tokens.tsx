import { useQuery } from "../data/use-query";
import { tokenTarget } from "../model/lookup";
import { useExplorerPaths } from "../network/active";
import { Amount } from "../ui/amount";
import { Card } from "../ui/card";
import { LookupForm } from "../ui/lookup-form";
import { ModuleActivity } from "../ui/module-activity";
import { Page } from "../ui/page";
import { Query } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { useKeyPaged } from "../ui/use-key-paged";
import { TokenList } from "./tokens/token-list";

/** `/explorer/tokens` */
export function TokensPage() {
  useDocumentTitle("Tokens");
  const paths = useExplorerPaths();
  const paged = useKeyPaged((s, q) => s.assets.getTokens(q), []);
  const params = useQuery((s) => s.assets.getTokenParams(), []);
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Tokens</h1>
        <p className="mt-1 max-w-[75ch] text-sm text-muted">
          Anyone can create a token with x/token. Each one is a denom of the form factory/&lt;creator&gt;/&lt;subdenom&gt; and lives in the
          bank beside ORAMA. The chain keeps no token units or decimals, so supplies are whole units of the token itself.
        </p>
      </div>
      <Card title="Open a token">
        <LookupForm
          label="Token denom"
          placeholder="factory/orama1…/name, or orama1…/name"
          resolve={(text) => tokenTarget(text, paths.assets)}
          hint="A token is named factory/ then its creator's orama1… address, a slash and a lowercase name."
        />
      </Card>
      <TokenList paged={paged} />
      <ModuleActivity title="Recent token activity" modules={["Tokens"]} empty="None of the newest transactions touched a token." />
      <Query state={params.state} onRetry={params.refetch} loading={<div className="h-10" />}>
        {(p) => (
          <p className="text-xs text-muted">
            Creating a token burns <Amount norama={p.creationFee} /> and locks {p.depositPerByte} norama for each byte of its subdenom, name, symbol and description.
          </p>
        )}
      </Query>
    </Page>
  );
}
