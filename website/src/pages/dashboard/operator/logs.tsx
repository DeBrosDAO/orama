import { useState } from "react";
import { PageHeader } from "../components/page-header";
import { CliCommandDisplay } from "../components/cli-command";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { NODES } from "../data/mock-data";

const SERVICES = ["node", "gateway", "rqlite", "olric", "ipfs"];

const MOCK_LOGS = [
  { time: "14:23:01", level: "INFO", message: "Request handled: GET /v1/health → 200 (1.2ms)" },
  { time: "14:23:02", level: "INFO", message: "Raft heartbeat received from leader 10.0.0.1" },
  { time: "14:23:05", level: "INFO", message: "Cache hit: session:abc (128B)" },
  { time: "14:23:08", level: "DEBUG", message: "WireGuard keepalive sent to 10.0.0.2" },
  { time: "14:23:10", level: "INFO", message: "Deployment sync: my-app v1.4.2 verified" },
  { time: "14:23:12", level: "INFO", message: "IPFS pin verified: bafybei...a3f2 (3 replicas)" },
  { time: "14:23:15", level: "WARN", message: "Slow query: SELECT * FROM logs (245ms)" },
  { time: "14:23:18", level: "INFO", message: "DNS record updated: my-app.orama.network → 10.0.0.1" },
  { time: "14:23:20", level: "INFO", message: "Function invoked: resize-image (32ms, 200)" },
  { time: "14:23:22", level: "INFO", message: "TLS certificate renewed: myapp.com (Caddy auto)" },
];

function levelColor(level: string) {
  switch (level) {
    case "ERROR": return "text-red-400";
    case "WARN": return "text-amber-400";
    case "DEBUG": return "text-muted/50";
    default: return "text-muted";
  }
}

export default function LogsPage() {
  const [selectedNode, setSelectedNode] = useState(NODES[0]?.name ?? "");
  const [selectedService, setSelectedService] = useState(SERVICES[0] ?? "");

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Logs" />

      {/* Filters */}
      <div className="flex flex-wrap gap-3">
        <div className="flex flex-col gap-1">
          <label className="font-mono text-[10px] text-muted uppercase tracking-wider">Node</label>
          <select
            value={selectedNode}
            onChange={(e) => setSelectedNode(e.target.value)}
            className="px-3 py-1.5 bg-bg border border-dashed border-border rounded-sm font-mono text-xs text-fg outline-none focus:border-accent"
          >
            {NODES.map((n) => <option key={n.name} value={n.name}>{n.name}</option>)}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className="font-mono text-[10px] text-muted uppercase tracking-wider">Service</label>
          <select
            value={selectedService}
            onChange={(e) => setSelectedService(e.target.value)}
            className="px-3 py-1.5 bg-bg border border-dashed border-border rounded-sm font-mono text-xs text-fg outline-none focus:border-accent"
          >
            {SERVICES.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </div>
      </div>

      {/* Log stream */}
      <DashedPanel className="p-0">
        {/* Terminal header */}
        <div className="flex items-center gap-2 px-4 py-2.5 border-b border-border">
          <div className="flex items-center gap-1.5">
            <span className="w-2.5 h-2.5 rounded-full bg-red-500" />
            <span className="w-2.5 h-2.5 rounded-full bg-amber-500" />
            <span className="w-2.5 h-2.5 rounded-full bg-green-500" />
          </div>
          <span className="text-xs font-mono text-muted ml-2">{selectedNode} / {selectedService}</span>
        </div>
        <div className="p-4 font-mono text-xs leading-relaxed space-y-0.5 max-h-96 overflow-y-auto bg-bg/50">
          {MOCK_LOGS.map((log, i) => (
            <div key={i} className="flex gap-2">
              <span className="text-muted/50 shrink-0">{log.time}</span>
              <span className={`shrink-0 w-12 ${levelColor(log.level)}`}>{log.level.padEnd(5)}</span>
              <span className="text-muted">{log.message}</span>
            </div>
          ))}
        </div>
      </DashedPanel>

      <CliCommandDisplay
        commands={[
          { description: "Stream node service logs", command: `sudo orama node logs ${selectedService}` },
          { description: "Stream deployment logs", command: "orama app logs my-app --follow" },
          { description: "Stream function logs", command: "orama function logs resize-image" },
        ]}
      />
    </div>
  );
}
