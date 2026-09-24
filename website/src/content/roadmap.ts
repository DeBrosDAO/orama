import { SERVICES } from "./services";

/**
 * The roadmap is three steps, in order. Nothing is dated: each step ships
 * when it is done. The funding plan (funding.ts) maps them onto months.
 */
export type MilestoneState = "done" | "next" | "later";

export interface Milestone {
  id: string;
  step: string;
  title: string;
  line: string;
  state: MilestoneState;
  points: string[];
}

export const MILESTONES: Milestone[] = [
  {
    id: "poc",
    step: "Done",
    title: "Working proof of concept",
    line: "The whole stack runs, and a real app runs on it.",
    state: "done",
    points: [
      `${SERVICES.length} services live`,
      "AnChat running on it (open beta)",
      "Devnet & testnet running",
    ],
  },
  {
    id: "stable",
    step: "01",
    title: "Stable",
    line: "Harden the network until it is boring.",
    state: "next",
    points: [
      "Close every self-audit finding",
      "Safe automatic node updates",
      "Independent security audit",
    ],
  },
  {
    id: "oramaos",
    step: "02",
    title: "OramaOS",
    line: "A locked-down operating system for every node.",
    state: "later",
    points: [
      "No remote login",
      "Encrypted disk, key split across nodes",
      "Updates itself safely",
    ],
  },
  {
    id: "orama-one",
    step: "03",
    title: "Orama One",
    line: "Our own node hardware, in people's hands.",
    state: "later",
    points: [
      "Plug in, join the network",
      "Built and shipped to the public",
      "Anyone can run part of the cloud",
    ],
  },
];
