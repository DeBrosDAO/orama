import { StrictMode, lazy, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Routes, Route } from "react-router";
import "./index.css";
import { Shell } from "./components/layout/shell";
import { LoadingSpinner } from "./components/ui/loading-spinner";
import { BTCPriceProvider } from "./components/ui/btc-price";

const Home = lazy(() => import("./pages/home"));
const Investors = lazy(() => import("./pages/investors"));
const Invest = lazy(() => import("./pages/invest"));
const Token = lazy(() => import("./pages/token"));
const OperatorsLanding = lazy(() => import("./pages/operators-landing"));
const ContributorsLanding = lazy(() => import("./pages/contributors-landing"));
const Docs = lazy(() => import("./pages/docs"));
const Whitelist = lazy(() => import("./pages/whitelist"));
const Blockchain = lazy(() => import("./pages/blockchain"));
const Compute = lazy(() => import("./pages/compute"));
const WhitepaperPage = lazy(() => import("./pages/whitepaper"));

/* Dashboard layout + pages */
const DashboardLayout = lazy(() => import("./pages/dashboard/layout"));
const DashboardIndex = lazy(() => import("./pages/dashboard/index"));
const DevOverview = lazy(() => import("./pages/dashboard/developer/overview"));
const DevDeployments = lazy(() => import("./pages/dashboard/developer/deployments"));
const DevFunctions = lazy(() => import("./pages/dashboard/developer/functions"));
const DevDatabase = lazy(() => import("./pages/dashboard/developer/database"));
const DevStorage = lazy(() => import("./pages/dashboard/developer/storage"));
const DevCache = lazy(() => import("./pages/dashboard/developer/cache"));
const DevDns = lazy(() => import("./pages/dashboard/developer/dns"));
const DevVault = lazy(() => import("./pages/dashboard/developer/vault"));
const DevNamespace = lazy(() => import("./pages/dashboard/developer/namespace"));
const DevSettings = lazy(() => import("./pages/dashboard/developer/settings"));
const OpsOverview = lazy(() => import("./pages/dashboard/operator/overview"));
const OpsNodes = lazy(() => import("./pages/dashboard/operator/nodes"));
const OpsCluster = lazy(() => import("./pages/dashboard/operator/cluster"));
const OpsMonitoring = lazy(() => import("./pages/dashboard/operator/monitoring"));
const OpsLogs = lazy(() => import("./pages/dashboard/operator/logs"));
const OpsStaking = lazy(() => import("./pages/dashboard/operator/staking"));
const OpsRewards = lazy(() => import("./pages/dashboard/operator/rewards"));
const OpsSettings = lazy(() => import("./pages/dashboard/operator/settings"));

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BTCPriceProvider>
    <BrowserRouter>
      <Suspense
        fallback={
          <div className="flex items-center justify-center min-h-screen bg-surface">
            <LoadingSpinner />
          </div>
        }
      >
        <Routes>
          {/* Dashboard — standalone layout with sidebar */}
          <Route path="dashboard" element={<DashboardLayout />}>
            <Route index element={<DashboardIndex />} />
            <Route path="dev/overview" element={<DevOverview />} />
            <Route path="dev/deployments" element={<DevDeployments />} />
            <Route path="dev/functions" element={<DevFunctions />} />
            <Route path="dev/database" element={<DevDatabase />} />
            <Route path="dev/storage" element={<DevStorage />} />
            <Route path="dev/cache" element={<DevCache />} />
            <Route path="dev/dns" element={<DevDns />} />
            <Route path="dev/vault" element={<DevVault />} />
            <Route path="dev/namespace" element={<DevNamespace />} />
            <Route path="dev/settings" element={<DevSettings />} />
            <Route path="ops/overview" element={<OpsOverview />} />
            <Route path="ops/nodes" element={<OpsNodes />} />
            <Route path="ops/cluster" element={<OpsCluster />} />
            <Route path="ops/monitoring" element={<OpsMonitoring />} />
            <Route path="ops/logs" element={<OpsLogs />} />
            <Route path="ops/staking" element={<OpsStaking />} />
            <Route path="ops/rewards" element={<OpsRewards />} />
            <Route path="ops/settings" element={<OpsSettings />} />
          </Route>

          {/* Invest dashboard — standalone (no Shell) */}
          <Route path="invest" element={<Invest />} />

          {/* Pages with Shell (navbar + footer) */}
          <Route element={<Shell />}>
            <Route index element={<Home />} />
            <Route path="investors" element={<Investors />} />
            <Route path="token" element={<Token />} />
            <Route path="operators" element={<OperatorsLanding />} />
            <Route path="contributors" element={<ContributorsLanding />} />
            <Route path="docs/*" element={<Docs />} />
            <Route path="whitelist" element={<Whitelist />} />
            <Route path="blockchain" element={<Blockchain />} />
            <Route path="compute" element={<Compute />} />
            <Route path="whitepaper" element={<WhitepaperPage />} />
          </Route>
        </Routes>
      </Suspense>
    </BrowserRouter>
    </BTCPriceProvider>
  </StrictMode>,
);
