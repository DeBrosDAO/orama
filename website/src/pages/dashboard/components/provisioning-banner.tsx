import { AlertTriangle, Loader2 } from "lucide-react";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { useNamespace } from "../context/namespace-context";

export function ProvisioningBanner() {
  const { activeNamespace } = useNamespace();
  if (!activeNamespace || activeNamespace.cluster_status === "ready") return null;

  const config = {
    provisioning: {
      icon: Loader2,
      iconClass: "animate-spin text-amber-500",
      title: "Namespace is provisioning",
      description: "Your namespace cluster is being set up. This usually takes 2-5 minutes.",
    },
    degraded: {
      icon: AlertTriangle,
      iconClass: "text-amber-500",
      title: "Namespace is degraded",
      description: "Some services may be unavailable. The cluster is being repaired.",
    },
    failed: {
      icon: AlertTriangle,
      iconClass: "text-red-500",
      title: "Namespace provisioning failed",
      description: "Cluster setup failed. Try deleting and recreating the namespace.",
    },
    none: {
      icon: AlertTriangle,
      iconClass: "text-muted",
      title: "No cluster",
      description: "This namespace has no cluster. Re-authenticate to trigger provisioning.",
    },
  }[activeNamespace.cluster_status];

  if (!config) return null;
  const Icon = config.icon;

  return (
    <DashedPanel className="p-4">
      <div className="flex items-start gap-3">
        <Icon size={16} className={config.iconClass} />
        <div>
          <h4 className="font-mono text-xs font-semibold text-fg uppercase tracking-wider">{config.title}</h4>
          <p className="text-xs text-muted mt-1">{config.description}</p>
        </div>
      </div>
    </DashedPanel>
  );
}
