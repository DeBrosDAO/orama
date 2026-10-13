import { formatRelative, formatUtc } from "../model/time";
import { useNow } from "./use-now";

/** "14 s ago", with the exact UTC time on hover. */
export function RelTime({ iso, className }: { iso: string; className?: string }) {
  const now = useNow();
  return (
    <time dateTime={iso} title={formatUtc(iso)} className={className}>
      {formatRelative(iso, now)}
    </time>
  );
}
