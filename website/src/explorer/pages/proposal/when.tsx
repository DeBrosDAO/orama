import { untilText } from "../../model/governance";
import { formatUtc } from "../../model/time";
import { Absent } from "../../ui/facts";
import { RelTime } from "../../ui/rel-time";
import { useNow } from "../../ui/use-now";

/** A time on a proposal's timeline: how long ago it was, or how long until it, and the exact time. */
export function When({ iso }: { iso: string | null }) {
  const now = useNow();
  if (iso === null) return <Absent>Not set</Absent>;
  return (
    <>
      {Date.parse(iso) > now ? <time dateTime={iso}>{untilText(iso, now)}</time> : <RelTime iso={iso} />}{" "}
      <span className="text-muted tabular-nums">({formatUtc(iso)})</span>
    </>
  );
}
