import { cn } from "../../lib/utils";

export interface ChipOption<T extends string> {
  id: T;
  label: string;
}

export interface ChipGroupProps<T extends string> {
  options: readonly ChipOption<T>[];
  value: T;
  onChange: (id: T) => void;
  label: string;
  className?: string;
}

/** A row of exclusive filter buttons. */
export function ChipGroup<T extends string>({ options, value, onChange, label, className }: ChipGroupProps<T>) {
  return (
    <div role="group" aria-label={label} className={cn("flex flex-wrap gap-2", className)}>
      {options.map((o) => (
        <Chip key={o.id} active={o.id === value} onClick={() => onChange(o.id)}>
          {o.label}
        </Chip>
      ))}
    </div>
  );
}

export function Chip({
  active = false,
  onClick,
  children,
}: {
  active?: boolean;
  onClick?: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-[13px] transition-colors cursor-pointer",
        active
          ? "border-fg bg-fg text-bg font-medium"
          : "border-border bg-surface-2 text-muted hover:text-fg hover:border-fg/30",
      )}
    >
      {children}
    </button>
  );
}
