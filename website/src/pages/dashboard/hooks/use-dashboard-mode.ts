import { useLocation } from "react-router";

export function useDashboardMode() {
  const { pathname } = useLocation();
  const segments = pathname.replace(/^\/dashboard\/?/, "").split("/");
  return {
    mode: (segments[0] as "dev" | "ops") || "dev",
    section: segments[1] || "overview",
  };
}
