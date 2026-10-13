import { Link } from "react-router";
import { useExplorerPaths } from "../../network/active";
import { Card } from "../../ui/card";
import { PrivacyBadge } from "../../ui/privacy-badge";
import { FieldList } from "./message-fields";
import type { PrivacyNotes } from "./privacy-notes";

/** For a transaction that uses the shielded pool: what the chain shows of it, and what it does not carry. */
export function PrivacyCard({ notes }: { notes: PrivacyNotes }) {
  const paths = useExplorerPaths();
  return (
    <Card title={notes.title} action={<PrivacyBadge privacy={notes.privacy} />}>
      <div className="grid gap-5 md:grid-cols-2">
        <div>
          <h3 className="mb-2 text-xs font-medium text-muted">On chain</h3>
          <FieldList fields={notes.shown} className="text-[13px]" />
        </div>
        <div>
          <h3 className="mb-2 text-xs font-medium text-muted">Not on chain</h3>
          <ul className="list-disc space-y-1 pl-5 text-[13px]">
            {notes.hidden.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </div>
      </div>
      <p className="mt-4 text-xs text-muted">
        The explorer shows only what the chain makes public.{" "}
        <Link to={paths.assets.shielded} className="underline underline-offset-4 hover:text-fg">
          The shielded pool
        </Link>{" "}
        has the pool's totals and the rest of its activity.
      </p>
    </Card>
  );
}
