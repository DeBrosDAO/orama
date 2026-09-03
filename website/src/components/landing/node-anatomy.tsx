import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

interface Tier {
  label: string;
  note: string;
  items: { name: string; detail: string }[];
  accent?: boolean;
}

/**
 * What is actually running on a node. The services grid above says what a
 * developer gets; this says what an operator runs — and it is the honest
 * answer to "what is Orama built out of".
 */
const TIERS: Tier[] = [
  {
    label: "Public edge",
    note: "The only ports the firewall opens to the internet",
    accent: true,
    items: [
      { name: "orama-sni-router", detail: "owns :443, routes by TLS SNI without decrypting" },
      { name: "caddy", detail: "TLS termination and ACME, moved to loopback" },
      { name: "coredns", detail: ":53 on nameserver nodes, zone backed by SQL" },
      { name: "orama-turn", detail: "TURN + TURNS relay, incl. the stealth listener" },
      { name: "wireguard", detail: ":51820 — the way in to everything below" },
    ],
  },
  {
    label: "Cluster services",
    note: "Bound to the overlay or loopback — never public",
    items: [
      { name: "orama-gateway", detail: "the control plane, :6001" },
      { name: "orama-node", detail: "libp2p host, RQLite manager, mesh + swarm sync" },
      { name: "rqlited", detail: "SQLite behind Raft — the cluster's shared state" },
      { name: "orama-olric", detail: "distributed cache, encrypted memberlist gossip" },
      { name: "ipfs + ipfs-cluster", detail: "private swarm, coordinated pinning" },
      { name: "orama-sfu", detail: "WebRTC media forwarding, overlay-bound only" },
      { name: "vault-guardian", detail: "Shamir share custody (see note below)" },
      { name: "anon", detail: "Anyone SOCKS5 client for anonymous egress" },
      { name: "ntfy", detail: "self-hosted push, fronted by Caddy" },
    ],
  },
  {
    label: "Tenant instances",
    note: "systemd template units — one set per namespace, started on demand",
    items: [
      { name: "rqlite@ns", detail: "the tenant's own Raft group" },
      { name: "olric@ns", detail: "the tenant's own cache ring" },
      { name: "gateway@ns", detail: "the tenant's data plane" },
      { name: "sfu@ns · turn@ns", detail: "when the namespace enables realtime" },
    ],
  },
  {
    label: "Deployment processes",
    note: "Supervised by the gateway, not by systemd",
    items: [
      { name: "go · nodejs · nextjs", detail: "supervised processes on allocated ports" },
      { name: "static", detail: "served straight from IPFS — no process at all" },
      { name: "go-wasm", detail: "run inside the serverless engine" },
    ],
  },
];

export function NodeAnatomy() {
  return (
    <>
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Every node runs the same thing."
              subtitle="There is no coordinator machine and no privileged node. A node differs from its peers only in flags — whether it answers DNS, whether its operator opted into running an anonymity relay, and which tenants the scheduler placed on it."
            />

            <div className="flex flex-col gap-3">
              {TIERS.map((tier) => (
                <div
                  key={tier.label}
                  className={
                    "border border-dashed p-4 sm:p-5 " +
                    (tier.accent
                      ? "border-accent/40 bg-accent/[0.03]"
                      : "border-border")
                  }
                >
                  <div className="flex flex-col sm:flex-row sm:items-baseline sm:justify-between gap-1 mb-3.5">
                    <span className="font-mono text-[10px] tracking-widest uppercase text-fg">
                      {tier.label}
                    </span>
                    <span className="font-mono text-[10px] text-muted">
                      {tier.note}
                    </span>
                  </div>

                  <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-6 gap-y-2.5">
                    {tier.items.map((item) => (
                      <div key={item.name} className="flex flex-col">
                        <span className="font-mono text-xs text-fg">
                          {item.name}
                        </span>
                        <span className="text-xs text-muted leading-snug">
                          {item.detail}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>

            <p className="text-sm text-muted leading-relaxed max-w-3xl">
              Only the first band is reachable from the internet. Everything
              below it binds to a WireGuard address or to loopback, and is
              reachable only through the tunnel or from the node itself. The
              vault guardian ships on every node but its authentication and peer
              discovery are still being reworked — it is not yet a service to
              trust with a secret.
            </p>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>
    </>
  );
}
