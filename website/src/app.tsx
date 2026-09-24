import { lazy, Suspense } from "react";
import type { ComponentType, LazyExoticComponent } from "react";
import { Routes, Route } from "react-router";
import { Shell } from "./components/layout/shell";
import { LoadingSpinner } from "./components/ui/loading-spinner";
import { DOCS_PATH, ROUTES } from "./content/routes";
import type { RouteKey } from "./content/routes";

/**
 * One page component per public route. The Record type makes this exhaustive:
 * a route added to ROUTES without a page here is a compile error, not a page
 * the prerenderer silently renders as the 404.
 */
const PAGES: Record<RouteKey, LazyExoticComponent<ComponentType>> = {
  home: lazy(() => import("./pages/home")),
  platform: lazy(() => import("./pages/platform")),
  howItWorks: lazy(() => import("./pages/how-it-works")),
  useCases: lazy(() => import("./pages/use-cases")),
  apps: lazy(() => import("./pages/apps")),
  roadmap: lazy(() => import("./pages/roadmap")),
  investors: lazy(() => import("./pages/investors")),
  donate: lazy(() => import("./pages/donate")),
  whitepaper: lazy(() => import("./pages/whitepaper")),
};

const Docs = lazy(() => import("./pages/docs"));
const NotFound = lazy(() => import("./pages/not-found"));

/** Nested routes are relative to the shell. */
const rel = (path: string) => path.replace(/^\//, "");

/** The route tree, shared by the browser entry and the prerenderer. */
export function App() {
  return (
    <Suspense
      fallback={
        <div className="flex items-center justify-center min-h-screen bg-surface">
          <LoadingSpinner />
        </div>
      }
    >
      <Routes>
        <Route element={<Shell />}>
          {(Object.keys(ROUTES) as RouteKey[]).map((key) => {
            const PageComponent = PAGES[key];
            const path = ROUTES[key].path;
            return path === "/" ? (
              <Route key={key} index element={<PageComponent />} />
            ) : (
              <Route key={key} path={rel(path)} element={<PageComponent />} />
            );
          })}
          <Route path={`${rel(DOCS_PATH)}/*`} element={<Docs />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </Suspense>
  );
}
