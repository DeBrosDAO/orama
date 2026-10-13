import { Link, useParams } from "react-router";
import { useQuery } from "../data/use-query";
import type { ContractInfo } from "../model/contracts";
import { parseAccount } from "../model/lookup";
import { shortAddress } from "../model/units";
import { useExplorerPaths } from "../network/active";
import { Card } from "../ui/card";
import { CopyChip } from "../ui/copy";
import { Absent, Facts } from "../ui/facts";
import { BlockLink, WalletLink } from "../ui/links";
import { Page } from "../ui/page";
import { NotFoundBox, Query, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { useKeyPaged } from "../ui/use-key-paged";
import { ContractAddressList } from "./contracts/address-list";
import { HistoryCard } from "./contract/history-card";
import { StateCard } from "./contract/state-card";

const NOT_FOUND_TITLE = "Contract not found";

function ContractView({ contract }: { contract: ContractInfo }) {
  const paths = useExplorerPaths();
  const history = useKeyPaged((s, q) => s.assets.getContractHistory(contract.address, q), [contract.address]);
  const state = useKeyPaged((s, q) => s.assets.getContractState(contract.address, q), [contract.address]);
  return (
    <>
      <header>
        <div className="text-[13px] text-muted">Contract</div>
        <h1 className="mt-1.5 font-display text-3xl font-semibold tracking-tight">{contract.label === "" ? shortAddress(contract.address) : contract.label}</h1>
        <p className="mt-1 flex flex-wrap items-center gap-2 text-sm text-muted">
          <span className="font-mono text-xs [overflow-wrap:anywhere]">{contract.address}</span>
          <CopyChip value={contract.address} label="Copy the contract address" />
        </p>
      </header>
      <Card title="Contract">
        <Facts
          rows={[
            [
              "Code",
              <Link to={paths.assets.code(contract.codeId)} className="font-mono underline decoration-dotted underline-offset-4">
                {contract.codeId}
              </Link>,
            ],
            ["Instantiated by", <WalletLink wallet={{ address: contract.creator }} />],
            ["Admin", contract.admin === null ? <Absent>None: the contract cannot be migrated</Absent> : <WalletLink wallet={{ address: contract.admin }} />],
            ["Created", contract.createdHeight > 0 ? <>in block <BlockLink height={contract.createdHeight} /></> : <Absent>Not recorded</Absent>],
          ]}
        />
        <p className="mt-3 text-xs text-muted">
          A contract is also an account:{" "}
          <Link to={paths.wallet(contract.address)} className="underline underline-offset-4 hover:text-fg">
            open it as a wallet
          </Link>{" "}
          for its balance and transactions.
        </p>
      </Card>
      <HistoryCard paged={history} />
      <StateCard paged={state} />
    </>
  );
}

/** An address that is no contract: say so, and list what it instantiated, since an account that does is the usual reason for the visit. */
function NotAContract({ address }: { address: string }) {
  const paths = useExplorerPaths();
  const created = useKeyPaged((s, q) => s.assets.getCreatedContracts(address, q), [address]);
  return (
    <>
      <NotFoundBox title={NOT_FOUND_TITLE} hint="This address is not a contract on this chain." />
      <p className="text-center text-sm">
        <Link to={paths.wallet(address)} className="underline underline-offset-4 hover:text-fg">
          Open it as a wallet
        </Link>
      </p>
      <ContractAddressList title="Contracts this address instantiated" empty="This address has not instantiated a contract." paged={created} />
    </>
  );
}

function ContractLoader({ address }: { address: string }) {
  const { state, refetch } = useQuery((s) => s.assets.getContract(address), [address]);
  useDocumentTitle(state.status === "ready" && state.data ? `Contract ${shortAddress(address)}` : "Contract");
  return (
    <Query state={state} subject="this contract" onRetry={refetch} loading={<Skeleton className="h-64 w-full" />}>
      {(contract) => (contract === null ? <NotAContract address={address} /> : <ContractView contract={contract} />)}
    </Query>
  );
}

/** `/explorer/contract/:address` */
export function ContractPage() {
  const address = parseAccount(useParams().address ?? "");
  useDocumentTitle(address === null ? NOT_FOUND_TITLE : null);
  return (
    <Page>
      {address === null ? (
        <NotFoundBox title={NOT_FOUND_TITLE} hint="A contract is an Orama address, which starts with orama1." />
      ) : (
        <ContractLoader key={address} address={address} />
      )}
    </Page>
  );
}
