import { describe, expect, it } from "vitest";
import { ErrorBoundary } from "./error-boundary";

describe("ErrorBoundary", () => {
  it("TestErrorBoundary_a_thrown_render_error_switches_to_the_fallback", () => {
    expect(ErrorBoundary.getDerivedStateFromError()).toEqual({ failed: true });
  });

  it("TestErrorBoundary_starts_healthy", () => {
    expect(new ErrorBoundary({ children: null }).state).toEqual({ failed: false });
  });
});
