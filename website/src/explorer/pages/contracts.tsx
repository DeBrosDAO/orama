import { contractTarget } from "../model/lookup";
import { useExplorerPaths } from "../network/active";
import { Card } from "../ui/card";
import { LookupForm } from "../ui/lookup-form";
import { ModuleActivity } from "../ui/module-activity";
import { Page } from "../ui/page";
import { useDocumentTitle } from "../ui/use-document-title";
import { useKeyPaged } from "../ui/use-key-paged";
import { CodeList } from "./contracts/code-list";

/** `/explorer/contracts` */
export function ContractsPage() {
  useDocumentTitle("Contracts");
  const paths = useExplorerPaths();
  const paged = useKeyPaged((s, q) => s.assets.getCodes(q), []);
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Contracts</h1>
        <p className="mt-1 max-w-[75ch] text-sm text-muted">
          CosmWasm contracts run from code that was stored once and can be instantiated many times. Each code lists its instances, and each contract
          shows its history and the raw state it stores. The explorer does not run a contract, so it shows what a contract holds, not what it answers.
        </p>
      </div>
      <Card title="Open a contract or a code">
        <LookupForm
          label="Contract address or code id"
          placeholder="Contract orama1…, or a code id"
          resolve={(text) => contractTarget(text, paths.assets)}
          hint="Paste a contract's orama1… address or a code's number."
        />
      </Card>
      <CodeList paged={paged} />
      <ModuleActivity title="Recent contract activity" modules={["Contracts"]} empty="None of the newest transactions touched a contract." />
    </Page>
  );
}
