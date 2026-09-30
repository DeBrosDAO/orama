import { useMemo } from "react";
import { useSource } from "../../data/provider";
import { activityToCsv, csvFilename } from "./csv";
import { BUTTON_CLASS } from "./constants";
import type { ActivityItem, WalletProfile } from "../../model/types";
import { Badge } from "../../ui/badge";
import { CopyChip } from "../../ui/copy";
import { Identicon } from "../../ui/identicon";

const IDENTICON_SIZE = 60;
const CSV_TYPE = "text/csv;charset=utf-8";
/** Some browsers cancel a download whose object URL is revoked in the same tick as the click. */
const REVOKE_DELAY_MS = 0;

function saveCsv(filename: string, csv: string): void {
  const url = URL.createObjectURL(new Blob([csv], { type: CSV_TYPE }));
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), REVOKE_DELAY_MS);
}

export interface WalletHeaderProps {
  profile: WalletProfile;
  /** The activity rows loaded so far: what "Export CSV" writes. */
  loaded: ActivityItem[];
}

export function WalletHeader({ profile, loaded }: WalletHeaderProps) {
  const { ref, roles } = profile;
  const demo = useSource().origin.kind === "demo";
  const csv = useMemo(() => (loaded.length > 0 ? activityToCsv(loaded, { demo }) : ""), [loaded, demo]);
  const count = loaded.length;
  return (
    <header className="flex flex-wrap items-start gap-4">
      <Identicon seed={ref.address} size={IDENTICON_SIZE} square />
      <div className="min-w-0 flex-1 basis-64">
        <h1 className="font-display text-2xl font-semibold tracking-tight">{ref.label ?? "Unlabeled wallet"}</h1>
        {(roles.length > 0 || ref.verified) && (
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            {roles.map((role) => (
              <Badge key={role}>{role}</Badge>
            ))}
            {ref.verified && <Badge tone="signal">Verified label</Badge>}
          </div>
        )}
        <div className="mt-2 flex items-start gap-2">
          <span className="break-all font-mono text-sm text-muted">{ref.address}</span>
          <CopyChip value={ref.address} label="Copy wallet address" className="shrink-0" />
        </div>
      </div>
      <div className="flex items-center gap-2">
        <span className="flex items-center gap-1.5 text-sm text-muted">
          Share
          <CopyChip value={window.location.href} label="Copy a link to this wallet page" withText />
        </span>
        <button
          type="button"
          className={BUTTON_CLASS}
          disabled={count === 0}
          title={count === 0 ? "No activity to export" : `Downloads the ${count} activity rows loaded so far`}
          onClick={() => saveCsv(csvFilename(ref.address, { demo }), csv)}
        >
          Export CSV
        </button>
      </div>
    </header>
  );
}
