import { useParams } from "react-router";
import { useQuery } from "../data/use-query";
import type { CodeInfo } from "../model/contracts";
import { permissionText } from "../model/contracts";
import { parseId } from "../model/lookup";
import { Card } from "../ui/card";
import { CopyChip } from "../ui/copy";
import { Absent, Facts } from "../ui/facts";
import { WalletLink } from "../ui/links";
import { Page } from "../ui/page";
import { NotFoundBox, Query, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { useKeyPaged } from "../ui/use-key-paged";
import { ContractAddressList } from "./contracts/address-list";

const NOT_FOUND_TITLE = "Code not found";

function CodeView({ code }: { code: CodeInfo }) {
  const contracts = useKeyPaged((s, q) => s.assets.getCodeContracts(code.codeId, q), [code.codeId]);
  return (
    <>
      <header>
        <div className="text-[13px] text-muted">Contract code</div>
        <h1 className="mt-1.5 font-display text-3xl font-semibold tracking-tight">Code {code.codeId}</h1>
      </header>
      <Card title="Code">
        <Facts
          rows={[
            ["Uploaded by", <WalletLink wallet={{ address: code.creator }} />],
            [
              "Checksum",
              <span className="inline-flex flex-wrap items-center gap-2">
                <span className="font-mono text-xs [overflow-wrap:anywhere]">{code.checksum}</span>
                <CopyChip value={code.checksum} label="Copy the code checksum" />
              </span>,
            ],
            ["Instantiation", permissionText(code.permission)],
            [
              "Allowed addresses",
              code.allowed.length === 0 ? (
                <Absent>None named</Absent>
              ) : (
                <ul className="space-y-1">
                  {code.allowed.map((a) => (
                    <li key={a}>
                      <WalletLink wallet={{ address: a }} />
                    </li>
                  ))}
                </ul>
              ),
            ],
          ]}
        />
      </Card>
      <ContractAddressList title="Contracts instantiated from this code" empty="No contract has been instantiated from this code." paged={contracts} />
    </>
  );
}

function CodeLoader({ id }: { id: number }) {
  const { state, refetch } = useQuery((s) => s.assets.getCode(id), [id]);
  useDocumentTitle(`Code ${id}`);
  return (
    <Query state={state} subject={`code ${id}`} onRetry={refetch} loading={<Skeleton className="h-64 w-full" />}>
      {(code) =>
        code === null ? (
          <NotFoundBox title={NOT_FOUND_TITLE} hint={`No code ${id} is stored on this chain. Codes are numbered from 1 in the order they were stored.`} />
        ) : (
          <CodeView code={code} />
        )
      }
    </Query>
  );
}

/** `/explorer/code/:id` */
export function CodePage() {
  const id = parseId(useParams().id ?? "");
  useDocumentTitle(id === null ? NOT_FOUND_TITLE : null);
  return (
    <Page>
      {id === null ? <NotFoundBox title={NOT_FOUND_TITLE} hint="A code is numbered from 1: /code/1." /> : <CodeLoader key={id} id={id} />}
    </Page>
  );
}
