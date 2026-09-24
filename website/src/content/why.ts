import type { LucideIcon } from "lucide-react";
import { EyeOff, Network, PowerOff, UsersRound } from "lucide-react";

/** Why a technology like this matters, beyond any one app. */
export interface Pillar {
  icon: LucideIcon;
  title: string;
  line: string;
}

export const PILLARS: Pillar[] = [
  {
    icon: PowerOff,
    title: "No kill switch",
    line: "Designed so no single company can switch your app off.",
  },
  {
    icon: EyeOff,
    title: "Private by default",
    line: "Wallet sign-in, encrypted links, calls that hide your address.",
  },
  {
    icon: Network,
    title: "No single point of failure",
    line: "One machine or one data center going down shouldn't take everything with it.",
  },
  {
    icon: UsersRound,
    title: "Owned by people",
    line: "Built for infrastructure in homes and communities, not three corporations.",
  },
];
