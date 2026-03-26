import type { ReactNode, ButtonHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export const SILVER = {
  light: "#d4d4d8",
  mid: "#a1a1aa",
  dark: "#71717a",
  shine: "#e4e4e7",
  bg: "rgba(161, 161, 170, 0.06)",
  border: "rgba(161, 161, 170, 0.2)",
  gradient: "linear-gradient(135deg, #d4d4d8 0%, #a1a1aa 40%, #71717a 100%)",
} as const;

export function SilverBadge({
  children,
  variant = "default",
  className,
}: {
  children: ReactNode;
  variant?: "default" | "outline" | "status";
  className?: string;
}) {
  const base =
    "inline-flex items-center px-2.5 py-0.5 text-xs font-mono tracking-wider";
  const variants = {
    default: "border border-dashed text-zinc-400",
    outline: "border border-dashed text-zinc-300",
    status: "border border-dashed text-zinc-200",
  };
  return (
    <span
      className={cn(base, variants[variant], className)}
      style={{
        borderColor:
          variant === "status"
            ? SILVER.light
            : variant === "outline"
              ? SILVER.mid
              : SILVER.dark,
      }}
    >
      {children}
    </span>
  );
}

export function SilverButton({
  children,
  variant = "primary",
  size = "lg",
  className,
  ...props
}: {
  children: ReactNode;
  variant?: "primary" | "ghost";
  size?: "default" | "lg";
  className?: string;
} & ButtonHTMLAttributes<HTMLButtonElement>) {
  const sizeClasses =
    size === "lg" ? "px-8 py-3 text-sm" : "px-5 py-2.5 text-xs";
  if (variant === "ghost") {
    return (
      <span
        className={cn(
          "inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase transition-all duration-200 rounded-sm cursor-pointer",
          "border text-zinc-400 hover:text-zinc-200 hover:bg-white/[0.03]",
          sizeClasses,
          className,
        )}
        style={{ borderColor: SILVER.dark }}
        {...(props as Record<string, unknown>)}
      >
        {children}
      </span>
    );
  }
  return (
    <span
      className={cn(
        "silver-button inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase transition-all duration-200 rounded-sm cursor-pointer text-black",
        sizeClasses,
        className,
      )}
      {...(props as Record<string, unknown>)}
    >
      {children}
    </span>
  );
}

export function SilverMetric({
  label,
  value,
}: {
  label: string;
  value: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs font-mono text-zinc-500 tracking-wider uppercase">
        {label}
      </span>
      <span
        className="text-2xl font-bold tabular-nums tracking-tight"
        style={{
          background: SILVER.gradient,
          WebkitBackgroundClip: "text",
          WebkitTextFillColor: "transparent",
        }}
      >
        {value}
      </span>
    </div>
  );
}
