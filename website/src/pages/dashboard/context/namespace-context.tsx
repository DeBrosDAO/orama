import { createContext, useContext, useCallback } from "react";
import type { ReactNode } from "react";
import { useLocalStorage } from "../../../hooks/useLocalStorage";
import { MOCK_NAMESPACES } from "../data/mock-data";

export interface Namespace {
  id: string;
  name: string;
  cluster_status: "none" | "provisioning" | "ready" | "degraded" | "failed";
}

interface NamespaceContextValue {
  namespaces: Namespace[];
  activeNamespace: Namespace | null;
  setActiveNamespace: (id: string) => void;
  createNamespace: (name: string) => void;
}

const NamespaceContext = createContext<NamespaceContextValue | null>(null);

export function NamespaceProvider({ children }: { children: ReactNode }) {
  const [namespaces, setNamespaces] = useLocalStorage<Namespace[]>(
    "orama-namespaces",
    MOCK_NAMESPACES,
  );
  const [activeId, setActiveId] = useLocalStorage<string | null>(
    "orama-active-namespace",
    MOCK_NAMESPACES[0]?.id ?? null,
  );

  const activeNamespace = namespaces.find((ns) => ns.id === activeId) ?? namespaces[0] ?? null;

  const setActiveNamespace = useCallback(
    (id: string) => {
      setActiveId(id);
    },
    [setActiveId],
  );

  const createNamespace = useCallback(
    (name: string) => {
      const ns: Namespace = {
        id: `ns-${Date.now()}`,
        name,
        cluster_status: "provisioning",
      };
      setNamespaces([...namespaces, ns]);
      setActiveId(ns.id);
    },
    [namespaces, setNamespaces, setActiveId],
  );

  return (
    <NamespaceContext.Provider
      value={{ namespaces, activeNamespace, setActiveNamespace, createNamespace }}
    >
      {children}
    </NamespaceContext.Provider>
  );
}

export function useNamespace() {
  const ctx = useContext(NamespaceContext);
  if (!ctx) throw new Error("useNamespace must be used within NamespaceProvider");
  return ctx;
}
