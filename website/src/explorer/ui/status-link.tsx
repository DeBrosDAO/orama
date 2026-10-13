import { ExternalLink } from "lucide-react";
import { useNetwork } from "../network/active";

/** The path of the gateway's public status page (docs: Developer, API surface, /status). */
const STATUS_PATH = "/status";

/** The URL of a network's public status page, or null for a network whose gateway is not known. */
export function statusUrl(gateway: string | null): string | null {
  return gateway === null ? null : `${gateway}${STATUS_PATH}`;
}

/** A link to the network's public monitoring page, where its gateway serves one. Renders nothing when the gateway is not known. */
export function StatusLink() {
  const url = statusUrl(useNetwork().gateway);
  if (url === null) return null;
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-[13px] hover:border-fg/30"
    >
      Network status page
      <ExternalLink size={13} aria-hidden="true" />
    </a>
  );
}
