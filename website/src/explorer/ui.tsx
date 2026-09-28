import type { ReactNode } from "react";
import { useState } from "react";
import { NavLink, Link, useNavigate } from "react-router";
import { cn } from "../lib/utils";
import oramaIcon from "../assets/orama-icon.png";

const NAV = [
  {
    label: "Chain",
    items: [
      ["Overview", "/explorer"],
      ["Epoch", "/explorer/epoch"],
      ["Supply", "/explorer/supply"],
    ],
  },
  {
    label: "People",
    items: [
      ["Committee", "/explorer/committee"],
    ],
  },
  {
    label: "Not in the binary yet",
    items: [
      ["Pools", "/explorer/pools"],
      ["Houses", "/explorer/houses"],
    ],
  },
] as const;

export function ExplorerFrame({ children }: { children: ReactNode }) {
  return (
    <div className="h-screen flex bg-black text-fg">
      <aside className="hidden md:flex w-60 shrink-0 flex-col border-r border-white/[0.06] bg-[#050505]">
        <Link to="/explorer" className="flex items-center gap-2.5 h-16 px-5">
          <img src={oramaIcon} alt="" className="h-7 w-7" />
          <span className="font-display text-sm font-bold tracking-[0.18em]">ORAMA</span>
        </Link>
        <div className="mx-4 mb-4 rounded-xl bg-white/[0.03] px-3 py-2.5">
          <div className="flex items-center gap-2">
            <span className="h-1.5 w-1.5 rounded-full bg-white/40" />
            <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-muted">Chain</span>
          </div>
          <p className="mt-1 text-[11px] leading-snug text-muted">Read through the gateway. The browser does not talk to the node.</p>
        </div>
        <nav className="flex-1 overflow-y-auto px-3 space-y-5">
          {NAV.map((group) => (
            <div key={group.label}>
              <p className="px-2 mb-1 font-mono text-[10px] uppercase tracking-[0.16em] text-muted/80">{group.label}</p>
              <ul className="space-y-0.5">
                {group.items.map(([label, href]) => (
                  <li key={href}>
                    <NavLink
                      to={href}
                      end={href === "/explorer"}
                      className={({ isActive }) =>
                        cn(
                          "block rounded-lg px-2.5 py-1.5 text-sm transition-colors",
                          isActive ? "bg-white/[0.08] text-fg" : "text-muted hover:text-fg hover:bg-white/[0.04]",
                        )
                      }
                    >
                      {label}
                    </NavLink>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-16 items-center gap-3 border-b border-white/[0.06] px-4 md:px-6">
          <Link to="/explorer" className="md:hidden font-display text-sm font-bold tracking-[0.16em]">
            ORAMA
          </Link>
          <SearchBox />
        </header>
        <main className="min-h-0 flex-1 overflow-y-auto px-4 py-5 md:px-6 md:py-6">{children}</main>
      </div>
    </div>
  );
}

export function SearchBox({ initial = "" }: { initial?: string }) {
  const navigate = useNavigate();
  const [q, setQ] = useState(initial);
  return (
    <form
      className="w-full"
      onSubmit={(e) => {
        e.preventDefault();
        const query = q.trim();
        if (!query) return;
        navigate(`/explorer/search?q=${encodeURIComponent(query)}`);
      }}
    >
      <input
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Search a block, transaction, wallet, or validator"
        aria-label="Search the chain"
        className="w-full rounded-full border border-white/[0.08] bg-white/[0.03] px-4 py-2.5 text-sm outline-none placeholder:text-muted/70 focus:border-white/20"
      />
    </form>
  );
}

export function Kicker({ children }: { children: ReactNode }) {
  return <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-muted">{children}</p>;
}

export function Panel({ children, className }: { children: ReactNode; className?: string }) {
  return <section className={cn("rounded-2xl border border-white/[0.06] bg-[#09090b]", className)}>{children}</section>;
}

export function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div>
      <Kicker>{label}</Kicker>
      <p className="mt-1 font-display text-2xl tabular-nums tracking-tight">{value}</p>
      {hint ? <p className="mt-1 text-xs text-muted">{hint}</p> : null}
    </div>
  );
}

export function Bar({ label, value, max, note }: { label: string; value: number; max: number; note: string }) {
  const width = `${Math.min(100, (value / max) * 100)}%`;
  return (
    <div>
      <div className="mb-1.5 flex justify-between text-xs text-muted">
        <span>{label}</span>
        <span className="font-mono text-fg">{note}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-white/[0.06]">
        <div className="h-full rounded-full bg-fg" style={{ width }} />
      </div>
    </div>
  );
}
