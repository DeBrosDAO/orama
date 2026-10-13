import { createContext, useContext } from "react";
import type { ReactNode } from "react";
import { explorerPaths } from "../model/routes";
import type { ExplorerPaths } from "../model/routes";
import type { Registry } from "./registry";
import type { Selection } from "./selection";

/** The network the URL names, with the registry it was chosen from. */
export interface ActiveNetwork extends Selection {
  registry: Registry;
}

const NetworkContext = createContext<ActiveNetwork | null>(null);

export function NetworkProvider({ value, children }: { value: ActiveNetwork; children: ReactNode }) {
  return <NetworkContext.Provider value={value}>{children}</NetworkContext.Provider>;
}

export function useNetwork(): ActiveNetwork {
  const network = useContext(NetworkContext);
  if (!network) throw new Error("useNetwork must be used inside <NetworkProvider>");
  return network;
}

/** The URL builders of the network being read; the default network's outside a provider, so a piece of the UI can render alone. */
export function useExplorerPaths(): ExplorerPaths {
  return useContext(NetworkContext)?.paths ?? explorerPaths;
}
