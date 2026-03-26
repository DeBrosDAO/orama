import {
  Rocket,
  Box,
  HardDrive,
  Shield,
  Globe,
  Database,
  Zap,
  Coins,
  Trophy,
  Server,
  Network,
  Settings,
  LayoutDashboard,
  FolderTree,
  Activity,
  ScrollText,
  BarChart3,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

export interface SidebarItem {
  id: string;
  label: string;
  icon: LucideIcon;
  path: string;
}

export const DEV_SIDEBAR: SidebarItem[] = [
  { id: "overview", label: "Overview", icon: LayoutDashboard, path: "/dashboard/dev/overview" },
  { id: "deployments", label: "Deployments", icon: Rocket, path: "/dashboard/dev/deployments" },
  { id: "functions", label: "Functions", icon: Box, path: "/dashboard/dev/functions" },
  { id: "database", label: "Database", icon: Database, path: "/dashboard/dev/database" },
  { id: "storage", label: "Storage", icon: HardDrive, path: "/dashboard/dev/storage" },
  { id: "cache", label: "Cache", icon: Zap, path: "/dashboard/dev/cache" },
  { id: "dns", label: "DNS", icon: Globe, path: "/dashboard/dev/dns" },
  { id: "vault", label: "Vault", icon: Shield, path: "/dashboard/dev/vault" },
  { id: "namespace", label: "Namespace", icon: FolderTree, path: "/dashboard/dev/namespace" },
  { id: "settings", label: "Settings", icon: Settings, path: "/dashboard/dev/settings" },
];

export const OPS_SIDEBAR: SidebarItem[] = [
  { id: "overview", label: "Overview", icon: LayoutDashboard, path: "/dashboard/ops/overview" },
  { id: "nodes", label: "Nodes", icon: Server, path: "/dashboard/ops/nodes" },
  { id: "cluster", label: "Cluster", icon: Network, path: "/dashboard/ops/cluster" },
  { id: "monitoring", label: "Monitoring", icon: Activity, path: "/dashboard/ops/monitoring" },
  { id: "logs", label: "Logs", icon: ScrollText, path: "/dashboard/ops/logs" },
  { id: "staking", label: "Staking", icon: Coins, path: "/dashboard/ops/staking" },
  { id: "rewards", label: "Rewards", icon: Trophy, path: "/dashboard/ops/rewards" },
  { id: "settings", label: "Settings", icon: BarChart3, path: "/dashboard/ops/settings" },
];
