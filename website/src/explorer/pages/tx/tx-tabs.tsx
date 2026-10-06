import { useRef, useState } from "react";
import type { KeyboardEvent, MutableRefObject, ReactNode } from "react";
import { cn } from "../../../lib/utils";
import { Card } from "../../ui/card";
import { nextTabIndex } from "./tab-nav";

export interface TabSpec {
  id: string;
  label: ReactNode;
  panel: ReactNode;
}

interface TabListProps {
  tabs: readonly TabSpec[];
  label: string;
  idPrefix: string;
  active: number;
  onSelect: (index: number) => void;
  onKeyDown: (e: KeyboardEvent<HTMLDivElement>) => void;
  buttons: MutableRefObject<(HTMLButtonElement | null)[]>;
}

function TabList({ tabs, label, idPrefix, active, onSelect, onKeyDown, buttons }: TabListProps) {
  return (
      <div role="tablist" aria-label={label} onKeyDown={onKeyDown} className="-mt-1 flex gap-5 border-b border-border">
        {tabs.map((t, i) => (
          <button
            key={t.id}
            ref={(el) => {
              buttons.current[i] = el;
            }}
            type="button"
            role="tab"
            id={`${idPrefix}-tab-${t.id}`}
            aria-selected={i === active}
            aria-controls={`${idPrefix}-panel-${t.id}`}
            tabIndex={i === active ? 0 : -1}
            onClick={() => onSelect(i)}
            className={cn(
              "-mb-px border-b-2 py-2 text-[13.5px] cursor-pointer",
              i === active ? "border-fg text-fg" : "border-transparent text-muted hover:text-fg",
            )}
          >
            {t.label}
          </button>
        ))}
      </div>
  );
}

/** Accessible tabs: arrows, Home and End move and select; only the active tab is in the tab order. */
export function TabsCard({ tabs, label, idPrefix }: { tabs: readonly TabSpec[]; label: string; idPrefix: string }) {
  const [active, setActive] = useState(0);
  const buttons = useRef<(HTMLButtonElement | null)[]>([]);

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    const next = nextTabIndex(active, tabs.length, e.key);
    if (next === null) return;
    e.preventDefault();
    setActive(next);
    buttons.current[next]?.focus();
  }

  const current = tabs[active];
  return (
    <Card>
      <TabList tabs={tabs} label={label} idPrefix={idPrefix} active={active} onSelect={setActive} onKeyDown={onKeyDown} buttons={buttons} />
      {current && (
        <div
          role="tabpanel"
          id={`${idPrefix}-panel-${current.id}`}
          aria-labelledby={`${idPrefix}-tab-${current.id}`}
          tabIndex={0}
          className={cn("pt-3.5")}
        >
          {current.panel}
        </div>
      )}
    </Card>
  );
}
