import { Link } from "react-router";
import { useExplorerPaths } from "../../network/active";
import type { ValidatorDetail } from "../../model/types";
import { formatUtc } from "../../model/time";
import { Badge } from "../../ui/badge";
import { CopyChip } from "../../ui/copy";
import { Identicon } from "../../ui/identicon";
import { statusWord } from "./logic";

const IDENTICON_SIZE = 56;

/** Who the validator is: name, state, operator wallet, and the words it wrote about itself (plain text, never a link). */
export function ValidatorHeader({ detail }: { detail: ValidatorDetail }) {
  const paths = useExplorerPaths();
  const { validator: v } = detail;
  return (
    <header className="space-y-3">
      <div className="flex flex-wrap items-start gap-4">
        <Identicon seed={v.ref.operator} size={IDENTICON_SIZE} square />
        <div className="min-w-0 flex-1 basis-64">
          <h1 className="font-display text-3xl font-semibold tracking-tight">{v.ref.moniker}</h1>
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            <Badge tone={v.status === "bonded" ? "ok" : "mute"}>{statusWord(v.status)}</Badge>
            {v.jailed && <Badge tone="bad">Jailed</Badge>}
            <Badge>{v.type === "committee" ? "Founding committee" : "Community stake"}</Badge>
          </div>
          <div className="mt-2 flex items-start gap-2">
            <span className="break-all font-mono text-sm text-muted">{v.ref.operator}</span>
            <CopyChip value={v.ref.operator} label="Copy the operator wallet address" className="shrink-0" />
          </div>
          <Link to={paths.wallet(v.ref.operator)} className="mt-1 inline-block text-sm underline decoration-dotted underline-offset-4 hover:text-fg">
            Open the operator wallet
          </Link>
        </div>
        <span className="flex items-center gap-1.5 text-sm text-muted">
          Share
          <CopyChip value={typeof window === "undefined" ? "" : window.location.href} label="Copy a link to this validator" withText />
        </span>
      </div>
      {v.jailed && (
        <p role="note" className="rounded-lg border border-loss/40 bg-loss/10 px-3 py-2 text-sm">
          This validator is jailed: it is out of the active set and signs no blocks until it is unjailed.
        </p>
      )}
      {!v.jailed && v.status !== "bonded" && (
        <p role="note" className="rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm">
          This validator is not in the active set.
          {detail.unbondsAt !== null && <> Its unbonding finishes {formatUtc(detail.unbondsAt)}.</>}
        </p>
      )}
      {(detail.details !== "" || detail.website !== "") && (
        <div className="max-w-[75ch] text-sm text-muted">
          {detail.details !== "" && <p>{detail.details}</p>}
          {detail.website !== "" && (
            <p className="mt-1">
              Website, as the validator wrote it: <span className="break-all font-mono text-[13px] text-fg">{detail.website}</span>
            </p>
          )}
        </div>
      )}
    </header>
  );
}
