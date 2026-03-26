import { Link } from "react-router";
import { Button } from "../../../components/ui/button";
import { cn } from "../../../lib/utils";
import { DEV_SIDEBAR, OPS_SIDEBAR } from "../data/navigation";
import { MOCK_ADDRESS } from "../data/mock-data";
import { useDashboardMode } from "../hooks/use-dashboard-mode";
import { NamespaceSelector } from "./namespace-selector";

export function Sidebar({ onDisconnect }: { onDisconnect: () => void }) {
  const { mode, section } = useDashboardMode();
  const sidebarItems = mode === "dev" ? DEV_SIDEBAR : OPS_SIDEBAR;

  return (
    <>
      {/* Mobile horizontal nav */}
      <div className="md:hidden fixed top-0 left-0 right-0 z-30 flex flex-col border-b border-dashed border-border bg-bg/95 backdrop-blur-sm">
        {/* Namespace + mode toggle row */}
        <div className="flex items-center gap-2 px-4 py-2 border-b border-dashed border-border">
          <div className="flex-1 min-w-0">
            <NamespaceSelector />
          </div>
          <div className="flex p-0.5 bg-surface-2/80 rounded-sm border border-border/60 shrink-0">
            <Link
              to="/dashboard/dev/overview"
              className={cn(
                "px-2 py-1 text-[10px] font-mono tracking-wider uppercase rounded-sm transition-colors",
                mode === "dev" ? "bg-accent text-white" : "text-muted",
              )}
            >
              Dev
            </Link>
            <Link
              to="/dashboard/ops/overview"
              className={cn(
                "px-2 py-1 text-[10px] font-mono tracking-wider uppercase rounded-sm transition-colors",
                mode === "ops" ? "bg-accent text-white" : "text-muted",
              )}
            >
              Ops
            </Link>
          </div>
        </div>
        {/* Nav items */}
        <div className="flex overflow-x-auto px-4 py-2 gap-1">
          {sidebarItems.map((item) => {
            const Icon = item.icon;
            return (
              <Link
                key={item.id}
                to={item.path}
                className={cn(
                  "flex items-center gap-1.5 px-3 py-1.5 text-[10px] font-mono tracking-wider uppercase whitespace-nowrap rounded-sm transition-colors shrink-0",
                  section === item.id ? "text-fg bg-white/[0.06]" : "text-muted",
                )}
              >
                <Icon size={12} />
                {item.label}
              </Link>
            );
          })}
        </div>
      </div>

      {/* Desktop sidebar */}
      <aside className="hidden md:flex w-56 shrink-0 border-r border-dashed border-border bg-surface/50 flex-col h-screen overflow-y-auto">
        {/* Namespace selector */}
        <div className="p-4 border-b border-dashed border-border">
          <NamespaceSelector />
        </div>

        {/* Mode toggle */}
        <div className="p-4 border-b border-dashed border-border">
          <div className="flex p-0.5 bg-surface-2/80 rounded-sm border border-border/60">
            <Link
              to="/dashboard/dev/overview"
              className={cn(
                "flex-1 px-2 py-1.5 text-[10px] font-mono tracking-wider uppercase rounded-sm transition-colors text-center",
                mode === "dev" ? "bg-accent text-white" : "text-muted hover:text-fg",
              )}
            >
              Developer
            </Link>
            <Link
              to="/dashboard/ops/overview"
              className={cn(
                "flex-1 px-2 py-1.5 text-[10px] font-mono tracking-wider uppercase rounded-sm transition-colors text-center",
                mode === "ops" ? "bg-accent text-white" : "text-muted hover:text-fg",
              )}
            >
              Operator
            </Link>
          </div>
        </div>

        {/* Nav items */}
        <nav className="flex flex-col gap-0.5 p-2">
          {sidebarItems.map((item) => {
            const Icon = item.icon;
            return (
              <Link
                key={item.id}
                to={item.path}
                className={cn(
                  "flex items-center gap-3 px-3 py-2 text-xs font-mono tracking-wider uppercase rounded-sm transition-colors text-left w-full",
                  section === item.id
                    ? "text-fg bg-white/[0.06]"
                    : "text-muted hover:text-fg hover:bg-white/[0.03]",
                )}
              >
                <Icon size={14} />
                {item.label}
              </Link>
            );
          })}
        </nav>

        {/* Wallet */}
        <div className="mt-auto p-4 border-t border-dashed border-border">
          <span className="font-mono text-xs text-muted block mb-2">{MOCK_ADDRESS}</span>
          <Button variant="ghost" size="sm" onClick={onDisconnect} className="w-full">
            Disconnect
          </Button>
        </div>
      </aside>
    </>
  );
}
