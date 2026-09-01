import { StrictMode, lazy, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Routes, Route } from "react-router";
import "./index.css";
import { Shell } from "./components/layout/shell";
import { LoadingSpinner } from "./components/ui/loading-spinner";

const Home = lazy(() => import("./pages/home"));
const Docs = lazy(() => import("./pages/docs"));
const NotFound = lazy(() => import("./pages/not-found"));

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <Suspense
        fallback={
          <div className="flex items-center justify-center min-h-screen bg-surface">
            <LoadingSpinner />
          </div>
        }
      >
        <Routes>
          <Route element={<Shell />}>
            <Route index element={<Home />} />
            <Route path="docs/*" element={<Docs />} />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </Suspense>
    </BrowserRouter>
  </StrictMode>,
);
