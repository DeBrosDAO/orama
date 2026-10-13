import { useParams } from "react-router";
import { useQuery } from "../data/use-query";
import { Page } from "../ui/page";
import { NotFoundBox, Query, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { NodeBonds } from "./node/node-bonds";
import { NodeFacts } from "./node/node-facts";
import { NodeHeader } from "./node/node-header";

function Loading() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-36 w-full" />
      <Skeleton className="h-48 w-full" />
      <Skeleton className="h-48 w-full" />
    </div>
  );
}

export function NodePage() {
  const { id = "" } = useParams();
  useDocumentTitle(`Node ${id}`);
  const { state, refetch } = useQuery((s) => s.getNode(id), [id]);
  return (
    <Page>
      <Query state={state} subject={`node ${id}`} onRetry={refetch} loading={<Loading />}>
        {(detail) =>
          detail === null ? (
            <NotFoundBox title="No such node" hint="The chain holds no node with that id. Open the node list to see the registered nodes." />
          ) : (
            <>
              <NodeHeader detail={detail} />
              <div className="grid gap-4 lg:grid-cols-2">
                <NodeBonds detail={detail} />
                <NodeFacts detail={detail} />
              </div>
            </>
          )
        }
      </Query>
    </Page>
  );
}
