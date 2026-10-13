import { Outlet } from "react-router";
import { Suspense } from "react";
import { LoadingSpinner } from "../ui/loading-spinner";
import { Navbar } from "../navigation/navbar";
import { Footer } from "../navigation/footer";
import { ScrollToTop } from "../ui/scroll-to-top";

export function Shell() {
  return (
    <div className="min-h-screen bg-surface text-fg blueprint-dots">
      <Navbar />
      <Suspense
        fallback={
          <div className="flex items-center justify-center min-h-screen">
            <LoadingSpinner />
          </div>
        }
      >
        <main className="pt-16">
          <Outlet />
        </main>
      </Suspense>
      <Footer />
      <ScrollToTop />
    </div>
  );
}
