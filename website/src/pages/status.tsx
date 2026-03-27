import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { StatusDot } from "../components/ui/status-dot";
import { MetricCard } from "../components/ui/metric-card";
import { Badge } from "../components/ui/badge";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";

/* ── Mock data ── */

type ServiceStatus = "operational" | "degraded" | "down" | "planned";

interface Service {
  name: string;
  status: ServiceStatus;
  uptime: string;
  responseTime: string;
}

const services: Service[] = [
  { name: "Gateway", status: "operational", uptime: "99.99%", responseTime: "12ms" },
  { name: "RQLite", status: "operational", uptime: "100%", responseTime: "8ms" },
  { name: "Olric", status: "operational", uptime: "99.98%", responseTime: "3ms" },
  { name: "IPFS", status: "operational", uptime: "99.95%", responseTime: "45ms" },
  { name: "WireGuard", status: "operational", uptime: "100%", responseTime: "1ms" },
  { name: "Serverless (WASM)", status: "operational", uptime: "99.97%", responseTime: "28ms" },
  { name: "Vault", status: "operational", uptime: "100%", responseTime: "5ms" },
  { name: "Deployments", status: "degraded", uptime: "99.90%", responseTime: "120ms" },
  { name: "BTC Bridge", status: "planned", uptime: "—", responseTime: "—" },
  { name: "Native DEX", status: "planned", uptime: "—", responseTime: "—" },
  { name: "AI Marketplace", status: "planned", uptime: "—", responseTime: "—" },
];

const nodeStats = { total: 12, healthy: 10, degraded: 1, offline: 1 };

/* ── Helpers ── */

function statusToDot(s: ServiceStatus) {
  if (s === "operational") return "active" as const;
  if (s === "degraded") return "warning" as const;
  if (s === "planned") return "warning" as const;
  return "error" as const;
}

function statusLabel(s: ServiceStatus) {
  if (s === "operational") return "Operational";
  if (s === "degraded") return "Degraded";
  if (s === "planned") return "Planned";
  return "Down";
}

function overallStatus(): { label: string; dot: "active" | "warning" | "error" } {
  const hasDown = services.some((s) => s.status === "down");
  const hasDegraded = services.some((s) => s.status === "degraded");
  if (hasDown) return { label: "Major Outage", dot: "error" };
  if (hasDegraded) return { label: "Partial Degradation", dot: "warning" };
  return { label: "All Systems Operational", dot: "active" };
}

/* ── Component ── */

export default function Status() {
  const overall = overallStatus();

  return (
    <Page title="System Status">
      {/* ── Hero ── */}
      <Section padding="wide">
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader title="System Status" />

            <DashedPanel className="p-6 sm:p-8">
              <div className="flex flex-col gap-3 items-center text-center">
                <div className="flex items-center gap-3">
                  <StatusDot status={overall.dot} />
                  <span className="font-display text-xl text-fg">
                    {overall.label}
                  </span>
                </div>
                <span className="text-muted text-xs font-mono">
                  Last updated: February 27, 2026 at 14:30 UTC
                </span>
              </div>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Node Overview ── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Node Overview"
              subtitle="Aggregate health across the network."
            />

            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
              <DashedPanel className="p-4">
                <MetricCard label="Total Nodes" value={String(nodeStats.total)} />
              </DashedPanel>
              <DashedPanel className="p-4">
                <div className="flex flex-col gap-1">
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">
                    Healthy
                  </span>
                  <span className="text-2xl font-bold text-accent-2 tabular-nums tracking-tight">
                    {nodeStats.healthy}
                  </span>
                </div>
              </DashedPanel>
              <DashedPanel className="p-4">
                <div className="flex flex-col gap-1">
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">
                    Degraded
                  </span>
                  <span className="text-2xl font-bold text-amber-500 tabular-nums tracking-tight">
                    {nodeStats.degraded}
                  </span>
                </div>
              </DashedPanel>
              <DashedPanel className="p-4">
                <div className="flex flex-col gap-1">
                  <span className="text-xs font-mono text-muted tracking-wider uppercase">
                    Offline
                  </span>
                  <span className="text-2xl font-bold text-red-500 tabular-nums tracking-tight">
                    {nodeStats.offline}
                  </span>
                </div>
              </DashedPanel>
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Service Grid ── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Services"
              subtitle="Real-time status of core infrastructure services."
            />

            <DashedPanel className="p-0 overflow-hidden">
              {/* Table header */}
              <div className="grid grid-cols-[1fr_auto_auto_auto] sm:grid-cols-[1fr_120px_100px_100px] gap-4 px-4 sm:px-6 py-3 border-b border-dashed border-border bg-surface-2/30">
                <span className="text-xs font-mono text-muted tracking-wider uppercase">
                  Service
                </span>
                <span className="text-xs font-mono text-muted tracking-wider uppercase text-right sm:text-left">
                  Status
                </span>
                <span className="text-xs font-mono text-muted tracking-wider uppercase text-right hidden sm:block">
                  Uptime
                </span>
                <span className="text-xs font-mono text-muted tracking-wider uppercase text-right hidden sm:block">
                  Latency
                </span>
              </div>

              {/* Rows */}
              {services.map((service, i) => (
                <div
                  key={service.name}
                  className={
                    "grid grid-cols-[1fr_auto_auto_auto] sm:grid-cols-[1fr_120px_100px_100px] gap-4 px-4 sm:px-6 py-4 items-center transition-colors hover:bg-surface-2/20" +
                    (i < services.length - 1 ? " border-b border-dashed border-border" : "")
                  }
                >
                  <span className="text-sm text-fg font-medium">{service.name}</span>

                  <div className="flex items-center gap-2 justify-end sm:justify-start">
                    <StatusDot status={statusToDot(service.status)} />
                    <span
                      className={
                        "text-xs font-mono " +
                        (service.status === "operational"
                          ? "text-accent-2"
                          : service.status === "degraded"
                            ? "text-amber-500"
                            : service.status === "planned"
                              ? "text-blue-400"
                              : "text-red-500")
                      }
                    >
                      {statusLabel(service.status)}
                    </span>
                  </div>

                  <div className="hidden sm:block text-right">
                    <Badge variant="status">{service.uptime}</Badge>
                  </div>

                  <span className="hidden sm:block font-mono text-xs text-muted text-right">
                    {service.responseTime}
                  </span>
                </div>
              ))}
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Incident History ── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader title="Incident History" />

            <DashedPanel className="p-8">
              <div className="flex flex-col items-center gap-3 text-center">
                <Badge variant="status">ALL CLEAR</Badge>
                <p className="text-muted text-sm">
                  No recent incidents reported in the last 90 days.
                </p>
              </div>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>
    </Page>
  );
}
