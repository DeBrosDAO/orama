import type { LucideIcon } from "lucide-react";
import {
  Blocks,
  Cpu,
  Gamepad2,
  GraduationCap,
  LifeBuoy,
  Megaphone,
  MessageSquareLock,
  Satellite,
  ShieldCheck,
  Smartphone,
  Users,
} from "lucide-react";

/**
 * What people can build. The tag is the honesty contract:
 *  - live:     an app does this on Orama today
 *  - ready:    every service it needs exists today
 *  - orama-one: needs Orama One hardware (placing an app on nearby machines,
 *              running a node at home) — on the roadmap, not yet possible
 */
export type UseCaseTag = "live" | "ready" | "orama-one";

export interface UseCase {
  id: string;
  icon: LucideIcon;
  title: string;
  line: string;
  tag: UseCaseTag;
  /** The services it draws on, by Service.id. */
  uses: string[];
  /** Pieces it uses that are not on the live service list (still in beta). */
  beta?: string[];
}

export const USE_CASE_TAG_LABEL: Record<UseCaseTag, string> = {
  live: "Live",
  ready: "Ready now",
  "orama-one": "With Orama One",
};

export const USE_CASES: UseCase[] = [
  {
    id: "messaging",
    icon: MessageSquareLock,
    title: "Private messaging",
    line: "AnChat: end-to-end encrypted chats and private calls, with no phone number.",
    tag: "live",
    uses: ["functions", "database", "realtime", "storage", "calls", "push"],
  },
  {
    id: "speak-freely",
    icon: Megaphone,
    title: "Speak freely",
    line: "Calls that look like ordinary web traffic, an anonymous proxy, no phone number. Built for journalists and activists.",
    tag: "ready",
    uses: ["calls", "proxy", "identity"],
  },
  {
    id: "web3",
    icon: Blocks,
    title: "Web3 apps with a real backend",
    line: "Wallet login, database, storage and functions, with no Amazon hiding behind the “decentralized” app.",
    tag: "ready",
    uses: ["identity", "database", "storage", "functions"],
  },
  {
    id: "degoogled",
    icon: Smartphone,
    title: "Phones without Google",
    line: "Push notifications that never pass through Google, for de-Googled Android.",
    tag: "ready",
    uses: ["push"],
  },
  {
    id: "realtime",
    icon: Gamepad2,
    title: "Live & multiplayer",
    line: "Games, voice rooms and real-time collaboration.",
    tag: "ready",
    uses: ["realtime", "calls", "cache"],
  },
  {
    id: "iot",
    icon: Cpu,
    title: "Sensors & devices",
    line: "Devices publish readings, functions react, the database remembers.",
    tag: "ready",
    uses: ["realtime", "functions", "database"],
  },
  {
    id: "community",
    icon: Users,
    title: "Community apps",
    line: "A town, a co-op or a club runs its own app. No big-tech bill, no off switch.",
    tag: "ready",
    uses: ["hosting", "database", "domains"],
  },
  {
    id: "backups",
    icon: ShieldCheck,
    title: "Secure backups",
    line: "A secret split into pieces across independent machines, so no single one can read it.",
    tag: "orama-one",
    uses: ["identity"],
    beta: ["Vault"],
  },
  {
    id: "satellite",
    icon: Satellite,
    title: "Satellite & remote places",
    line: "A village, a ship, a research station or a mountain clinic on satellite internet like Starlink. A node box runs apps on site; the satellite link joins it to the world.",
    tag: "orama-one",
    uses: ["hosting", "database", "realtime"],
  },
  {
    id: "disaster",
    icon: LifeBuoy,
    title: "Disaster response",
    line: "Bring a box. Rescue teams get a working local cloud when the networks are down.",
    tag: "orama-one",
    uses: ["hosting", "realtime", "calls"],
  },
  {
    id: "schools",
    icon: GraduationCap,
    title: "Places without data centers",
    line: "Schools and regions get infrastructure that is owned by the people who use it.",
    tag: "orama-one",
    uses: ["hosting", "database", "storage"],
  },
];
