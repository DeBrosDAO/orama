import { formatNorama, formatSigned } from "../model/units";
import { cn } from "../../lib/utils";

export interface AmountProps {
  norama: string;
  /** Show "+" or "−" and colour by direction. */
  signed?: boolean;
  /** Hide the trailing "ORAMA". */
  bare?: boolean;
  maxFraction?: number;
  className?: string;
}

/** An ORAMA amount. Truncated, never rounded; the full value is in the tooltip. */
export function Amount({ norama, signed = false, bare = false, maxFraction, className }: AmountProps) {
  const unit = bare ? "" : " ORAMA";
  if (!signed) {
    return (
      <span className={cn("font-mono tabular-nums", className)} title={`${norama} norama`}>
        {formatNorama(norama, maxFraction)}
        {unit}
      </span>
    );
  }
  const s = formatSigned(norama, maxFraction);
  return (
    <span
      className={cn("font-mono tabular-nums", s.sign === "+" && "text-gain", className)}
      title={`${norama} norama`}
    >
      {s.sign}
      {s.text}
      {unit}
    </span>
  );
}
