import { hashString } from "../model/hash";
import { cn } from "../../lib/utils";

export const HUE_RANGE = 360;

export interface IdenticonProps {
  /** Anything stable that identifies the thing: an address, a moniker. */
  seed: string;
  size?: number;
  /** Rounded square instead of a circle. */
  square?: boolean;
  className?: string;
}

/** A colour badge derived from the seed, so the same wallet always looks the same. */
export function Identicon({ seed, size = 18, square = false, className }: IdenticonProps) {
  const h = hashString(seed);
  const a = h % HUE_RANGE;
  const b = Math.floor(h / HUE_RANGE) % HUE_RANGE;
  return (
    <span
      aria-hidden="true"
      className={cn("inline-block shrink-0", square ? "rounded-2xl" : "rounded-full", className)}
      style={{
        width: size,
        height: size,
        background: `linear-gradient(135deg, hsl(${a} 42% 50%), hsl(${b} 46% 26%))`,
      }}
    />
  );
}
