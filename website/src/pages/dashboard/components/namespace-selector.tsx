import { useState } from "react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import * as Dialog from "@radix-ui/react-dialog";
import { ChevronDown, Plus, X } from "lucide-react";
import { StatusDot } from "../../../components/ui/status-dot";
import { Button } from "../../../components/ui/button";
import { useNamespace } from "../context/namespace-context";
import { cn } from "../../../lib/utils";
import type { Namespace } from "../context/namespace-context";

function statusToVariant(status: Namespace["cluster_status"]) {
  switch (status) {
    case "ready": return "active" as const;
    case "provisioning": return "warning" as const;
    case "degraded": return "warning" as const;
    case "failed": return "error" as const;
    default: return "neutral" as const;
  }
}

export function NamespaceSelector() {
  const { namespaces, activeNamespace, setActiveNamespace, createNamespace } = useNamespace();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [newName, setNewName] = useState("");

  const handleCreate = () => {
    const trimmed = newName.trim().toLowerCase().replace(/[^a-z0-9-]/g, "-");
    if (!trimmed) return;
    createNamespace(trimmed);
    setNewName("");
    setDialogOpen(false);
  };

  return (
    <>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <button
            type="button"
            className="flex items-center gap-2 w-full px-3 py-2 text-left rounded-sm border border-dashed border-border hover:border-border/80 transition-colors"
          >
            <StatusDot status={activeNamespace ? statusToVariant(activeNamespace.cluster_status) : "neutral"} />
            <span className="font-mono text-xs text-fg truncate flex-1">
              {activeNamespace?.name ?? "Select namespace"}
            </span>
            <ChevronDown size={12} className="text-muted shrink-0" />
          </button>
        </DropdownMenu.Trigger>

        <DropdownMenu.Portal>
          <DropdownMenu.Content
            align="start"
            sideOffset={4}
            className="z-50 min-w-[200px] border border-dashed border-border bg-surface rounded-sm p-1 shadow-lg"
          >
            <DropdownMenu.Label className="px-2 py-1.5 font-mono text-[10px] text-muted uppercase tracking-wider">
              Namespaces
            </DropdownMenu.Label>
            {namespaces.map((ns) => (
              <DropdownMenu.Item
                key={ns.id}
                onSelect={() => setActiveNamespace(ns.id)}
                className={cn(
                  "flex items-center gap-2 px-2 py-1.5 rounded-sm cursor-pointer outline-none",
                  "hover:bg-white/[0.06] focus:bg-white/[0.06]",
                  ns.id === activeNamespace?.id && "bg-white/[0.06]",
                )}
              >
                <StatusDot status={statusToVariant(ns.cluster_status)} />
                <span className="font-mono text-xs text-fg">{ns.name}</span>
              </DropdownMenu.Item>
            ))}
            <DropdownMenu.Separator className="h-px my-1 bg-border" />
            <DropdownMenu.Item
              onSelect={() => setDialogOpen(true)}
              className="flex items-center gap-2 px-2 py-1.5 rounded-sm cursor-pointer outline-none hover:bg-white/[0.06] focus:bg-white/[0.06] text-accent"
            >
              <Plus size={12} />
              <span className="font-mono text-xs">Create Namespace</span>
            </DropdownMenu.Item>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>

      <Dialog.Root open={dialogOpen} onOpenChange={setDialogOpen}>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50" />
          <Dialog.Content className="fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-sm border border-dashed border-border bg-surface rounded-sm p-6">
            <div className="flex items-center justify-between mb-6">
              <Dialog.Title className="font-display font-bold text-lg text-fg">
                Create Namespace
              </Dialog.Title>
              <Dialog.Close asChild>
                <button type="button" className="text-muted hover:text-fg">
                  <X size={16} />
                </button>
              </Dialog.Close>
            </div>
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <label className="font-mono text-xs text-muted uppercase tracking-wider">
                  Name
                </label>
                <input
                  type="text"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && handleCreate()}
                  placeholder="my-project"
                  className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent"
                />
                <span className="font-mono text-[10px] text-muted">
                  Lowercase letters, numbers, and hyphens only
                </span>
              </div>
              <Button onClick={handleCreate} className="w-full">
                Create
              </Button>
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}
