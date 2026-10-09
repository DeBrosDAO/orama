import { useEffect, useId, useRef } from "react";
import type { FocusEvent, KeyboardEvent } from "react";
import { Link, useLocation } from "react-router";
import { ChevronDown } from "lucide-react";
import { cn } from "../../lib/utils";
import { isActivePath } from "../../content/navigation";
import type { NavGroup } from "../../content/navigation";

/** Pointer leaving the menu closes it after this long, so a diagonal move to the panel doesn't. */
const HOVER_CLOSE_MS = 150;

export interface NavMenuProps {
  group: NavGroup;
  open: boolean;
  active: boolean;
  onOpenChange: (open: boolean) => void;
  triggerClass: string;
}

/**
 * One menu of the top bar, as a disclosure: a button that shows a panel of
 * links. The links are always in the prerendered HTML, hidden until opened,
 * so search engines find and follow them like any other link.
 */
export function NavMenu({ group, open, active, onOpenChange, triggerClass }: NavMenuProps) {
  const panelId = useId();
  const { pathname } = useLocation();
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const closeTimer = useRef<number | undefined>(undefined);
  const pressedWithMouse = useRef(false);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) onOpenChange(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open, onOpenChange]);

  useEffect(() => () => window.clearTimeout(closeTimer.current), []);

  const hoverOpen = () => {
    window.clearTimeout(closeTimer.current);
    onOpenChange(true);
  };
  const hoverClose = () => {
    window.clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => onOpenChange(false), HOVER_CLOSE_MS);
  };
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) {
      onOpenChange(false);
      buttonRef.current?.focus();
    }
  };
  const onBlur = (e: FocusEvent) => {
    if (!rootRef.current?.contains(e.relatedTarget as Node | null)) onOpenChange(false);
  };

  return (
    <div
      ref={rootRef}
      className="relative"
      onPointerEnter={(e) => e.pointerType === "mouse" && hoverOpen()}
      onPointerLeave={(e) => e.pointerType === "mouse" && hoverClose()}
      onKeyDown={onKeyDown}
      onBlur={onBlur}
    >
      <button
        ref={buttonRef}
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        onKeyDown={() => {
          pressedWithMouse.current = false;
        }}
        onPointerDown={(e) => {
          pressedWithMouse.current = e.pointerType === "mouse";
        }}
        // A mouse has already opened the menu by hovering, so its click
        // must not toggle it shut; touch and keyboard toggle.
        onClick={() => {
          onOpenChange(pressedWithMouse.current ? true : !open);
          pressedWithMouse.current = false;
        }}
        className={cn(triggerClass, "inline-flex items-center gap-1", (open || active) && "text-fg")}
      >
        {group.label}
        <ChevronDown size={12} aria-hidden="true" className={cn("transition-transform duration-150", open && "rotate-180")} />
      </button>

      <div
        id={panelId}
        className={cn(
          "absolute left-1/2 top-full -translate-x-1/2 pt-3 transition-all duration-150",
          open ? "visible opacity-100 translate-y-0" : "invisible opacity-0 -translate-y-1 pointer-events-none",
        )}
      >
        <ul className="w-72 rounded-2xl border border-border/60 bg-surface p-1.5 shadow-[0_12px_40px_rgba(0,0,0,0.5)]">
          {group.items.map((item) => {
            const current = isActivePath(pathname, item.path);
            return (
              <li key={item.path}>
                <Link
                  to={item.path}
                  aria-current={current ? "page" : undefined}
                  onClick={() => onOpenChange(false)}
                  className={cn(
                    "flex flex-col gap-0.5 rounded-xl px-3.5 py-2.5 transition-colors outline-none",
                    "hover:bg-white/[0.05] focus-visible:bg-white/[0.07]",
                    current && "bg-white/[0.04]",
                  )}
                >
                  <span className={cn("text-sm font-medium", current ? "text-fg" : "text-fg/90")}>{item.label}</span>
                  <span className="text-xs text-muted">{item.line}</span>
                </Link>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
