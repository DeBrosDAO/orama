import { Outlet } from "react-router";
import { Suspense, useState } from "react";
import { LoadingSpinner } from "../ui/loading-spinner";
import { WhitelistBanner } from "../navigation/whitelist-banner";
import { Navbar } from "../navigation/navbar";
import { Footer } from "../navigation/footer";
import { ScrollToTop } from "../ui/scroll-to-top";
import { FloatingCTA } from "../navigation/floating-cta";

export function Shell() {
  const [bannerVisible, setBannerVisible] = useState(true);

  return (
    <div
      className="min-h-screen bg-surface text-fg"
      style={{
        backgroundImage:
          "radial-gradient(circle, rgba(161,161,170,0.08) 1px, transparent 1px)",
        backgroundSize: "24px 24px",
      }}
    >
      <WhitelistBanner onDismiss={() => setBannerVisible(false)} />
      <Navbar bannerVisible={bannerVisible} />
      <Suspense
        fallback={
          <div className="flex items-center justify-center min-h-screen">
            <LoadingSpinner />
          </div>
        }
      >
        <main className="pt-16 md:pt-32">
          <Outlet />
        </main>
      </Suspense>
      <Footer />
      <FloatingCTA />
      <ScrollToTop />
    </div>
  );
}
