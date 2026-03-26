export type ProductStatus = "active" | "coming-soon" | "beta";

export interface Product {
  name: string;
  slug: string;
  tagline: string;
  description: string;
  tech: string;
  status: ProductStatus;
}

export const PRODUCTS: Product[] = [
  {
    name: "Orama Network",
    slug: "network",
    tagline: "Decentralized Cloud Infrastructure",
    description:
      "Edge proxy and API gateway with distributed SQL, caching, pub/sub, and serverless WASM execution across a global mesh of nodes.",
    tech: "core / go",
    status: "active",
  },
  {
    name: "Orama Vault",
    slug: "vault",
    tagline: "Zero-Knowledge Secret Storage",
    description:
      "Distributed secrets engine using Shamir's Secret Sharing. Splits secrets across nodes with information-theoretic security.",
    tech: "engine / zig",
    status: "active",
  },
  {
    name: "Network SDK",
    slug: "sdk",
    tagline: "One SDK, Every Service",
    description:
      "Isomorphic TypeScript SDK for all gateway operations — databases, pub/sub, vault, cache, storage, and serverless functions.",
    tech: "sdk / typescript",
    status: "active",
  },
  {
    name: "Playground",
    slug: "playground",
    tagline: "Interactive Service Demos",
    description:
      "Visual, hands-on learning environment for exploring Orama Network services with live code examples.",
    tech: "app / react",
    status: "active",
  },
  {
    name: "RootWallet",
    slug: "rootwallet",
    tagline: "Hybrid Hardware Wallet",
    description:
      "Multi-chain self-custody wallet for EVM and Solana. CLI, SDK, and mobile app with BIP-39 HD derivation and AES-256 encryption.",
    tech: "wallet / typescript",
    status: "active",
  },
];
