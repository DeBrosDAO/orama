import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { DashedPanel } from "../ui/dashed-panel";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { AnimateIn } from "../ui/animate-in";

const stacks = [
  {
    language: "Go",
    reason: "Performance, concurrency, and a rich systems ecosystem. Powers the gateway, CLI, and all core services.",
    items: [
      "API Gateway (net/http)",
      "RQLite integration (Raft)",
      "Olric cache (consistent hashing)",
      "WireGuard mesh controller",
      "CLI tooling (cobra)",
      "WASM runtime (wazero)",
    ],
  },
  {
    language: "Zig",
    reason: "Manual memory control and zero-overhead cryptography. Powers the Vault guardian daemon.",
    items: [
      "Vault guardian daemon",
      "Shamir's SSS (GF(2^8))",
      "AES-256-GCM encryption",
      "HMAC-SHA256 authentication",
      "Binary TCP protocol",
      "File-per-user storage",
    ],
  },
  {
    language: "TypeScript",
    reason: "Isomorphic code for browser and Node.js. Powers the SDK, RootWallet, and all frontend tooling.",
    items: [
      "Network SDK (browser + Node)",
      "RootWallet SDK",
      "Website (React + Vite)",
      "React hooks library",
      "CLI companion tools",
    ],
  },
];

export function ContribStack() {
  return (
    <>
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Tech Stack"
              subtitle="Three languages, each chosen for what it does best."
            />

            <div className="grid grid-cols-1 md:grid-cols-3 gap-0">
              {stacks.map((stack) => (
                <DashedPanel key={stack.language} className="p-6">
                  <div className="flex flex-col gap-4">
                    <h3 className="font-display font-bold text-fg text-lg">
                      {stack.language}
                    </h3>

                    <p className="text-xs text-muted leading-relaxed">
                      {stack.reason}
                    </p>

                    <ul className="flex flex-col gap-2">
                      {stack.items.map((item) => (
                        <li
                          key={item}
                          className="text-sm text-muted font-mono flex items-center gap-2"
                        >
                          <span className="text-accent text-xs">→</span>
                          {item}
                        </li>
                      ))}
                    </ul>
                  </div>
                </DashedPanel>
              ))}
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>
    </>
  );
}
