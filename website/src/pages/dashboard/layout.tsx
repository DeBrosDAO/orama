import { useState } from "react";
import { Outlet } from "react-router";
import { Page } from "../../components/layout/page";
import { Section } from "../../components/layout/section";
import { DashedPanel } from "../../components/ui/dashed-panel";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { NamespaceProvider } from "./context/namespace-context";
import { Sidebar } from "./components/sidebar";

function ConnectWallet({ onConnect }: { onConnect: () => void }) {
  return (
    <Section padding="wide">
      <div className="flex items-center justify-center min-h-[60vh]">
        <DashedPanel withCorners className="p-8 sm:p-12 max-w-md w-full">
          <div className="flex flex-col items-center gap-6 text-center">
            <svg width="48" height="48" viewBox="0 0 48 48" fill="none" className="text-accent/50">
              <line x1="24" y1="0" x2="24" y2="48" stroke="currentColor" strokeWidth="1" strokeDasharray="4 4" />
              <line x1="0" y1="24" x2="48" y2="24" stroke="currentColor" strokeWidth="1" strokeDasharray="4 4" />
              <circle cx="24" cy="24" r="8" stroke="currentColor" strokeWidth="1" />
            </svg>
            <h2 className="font-display font-bold text-xl text-fg">Connect Wallet</h2>
            <p className="text-muted text-sm leading-relaxed">
              Sign in with RootWallet to access the Orama dashboard.
            </p>
            <Button size="lg" onClick={onConnect}>Connect RootWallet</Button>
            <Badge variant="default">Demo mode — no real wallet connection</Badge>
          </div>
        </DashedPanel>
      </div>
    </Section>
  );
}

export default function DashboardLayout() {
  const [connected, setConnected] = useState(false);

  if (!connected) {
    return (
      <Page title="Dashboard">
        <ConnectWallet onConnect={() => setConnected(true)} />
      </Page>
    );
  }

  return (
    <NamespaceProvider>
      <Page title="Dashboard">
        <div className="flex h-screen">
          <Sidebar onDisconnect={() => setConnected(false)} />
          <main className="flex-1 overflow-y-auto pt-24 md:pt-0">
            <div className="max-w-5xl mx-auto px-4 sm:px-6 py-8">
              <Outlet />
            </div>
          </main>
        </div>
      </Page>
    </NamespaceProvider>
  );
}
