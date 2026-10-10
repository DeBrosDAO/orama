import type { LucideIcon } from "lucide-react";
import {
  AppWindow,
  Bell,
  Database,
  FunctionSquare,
  Globe,
  HardDrive,
  KeyRound,
  Radio,
  Video,
  VenetianMask,
  Zap,
} from "lucide-react";

/**
 * What the network offers today. Every entry here is shipped and in use by a
 * real tenant — this list is the site's claim of what Orama can do, so an
 * entry is added only when the code behind it works. The vault and OramaOS
 * are deliberately absent: they are partial and live on the roadmap instead.
 */
export interface Service {
  id: string;
  icon: LucideIcon;
  name: string;
  /** One plain-English line. */
  line: string;
  /**
   * The service people already know that this does the job of. Omitted when
   * the big clouds have no equivalent.
   */
  insteadOf?: string;
}

export const SERVICES: Service[] = [
  {
    id: "hosting",
    icon: AppWindow,
    name: "App hosting",
    line: "Put websites and apps online: static, Next.js, Node or Go.",
    insteadOf: "Vercel · EC2",
  },
  {
    id: "database",
    icon: Database,
    name: "Database",
    line: "Your own SQL database, kept in sync on three machines.",
    insteadOf: "RDS",
  },
  {
    id: "cache",
    icon: Zap,
    name: "Cache",
    line: "Fast in-memory storage for data you read all the time.",
    insteadOf: "ElastiCache",
  },
  {
    id: "storage",
    icon: HardDrive,
    name: "File storage",
    line: "Upload files to storage spread across the network's machines.",
    insteadOf: "S3",
  },
  {
    id: "realtime",
    icon: Radio,
    name: "Real-time messaging",
    line: "Push updates to every connected user, instantly.",
    insteadOf: "SNS · Pusher",
  },
  {
    id: "functions",
    icon: FunctionSquare,
    name: "Serverless functions",
    line: "Run code on demand, on a schedule, or when events happen.",
    insteadOf: "Lambda",
  },
  {
    id: "domains",
    icon: Globe,
    name: "Domains & HTTPS",
    line: "Every app gets an HTTPS address, with certificates that renew themselves.",
    insteadOf: "Route 53 · ACM",
  },
  {
    id: "calls",
    icon: Video,
    name: "Voice & video calls",
    line: "Relayed calls, so people don't have to see each other's IP address.",
    insteadOf: "Twilio",
  },
  {
    id: "push",
    icon: Bell,
    name: "Push notifications",
    line: "Push to phones, including Android without Google.",
    insteadOf: "Firebase",
  },
  {
    id: "proxy",
    icon: VenetianMask,
    name: "Anonymous proxy",
    line: "Send requests out through the Tor network.",
  },
  {
    id: "identity",
    icon: KeyRound,
    name: "Wallet login & keys",
    line: "Sign in with a wallet. Scoped API keys for your apps.",
    insteadOf: "Cognito · IAM",
  },
];
