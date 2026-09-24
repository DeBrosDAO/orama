import { Link } from "react-router";
import { ArrowRight } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { Button } from "../components/ui/button";

const NOT_FOUND_ROUTE = {
  path: "/404",
  title: "Page not found",
  description: "This page doesn't exist.",
};

export default function NotFound() {
  return (
    <Page route={NOT_FOUND_ROUTE} noindex>
      <Section padding="wide">
        <div className="flex flex-col items-center text-center gap-6 py-24">
          <span className="font-mono text-xs tracking-widest uppercase text-muted">404</span>
          <h1 className="font-display font-bold text-3xl md:text-4xl text-fg tracking-tight">
            This page doesn't exist.
          </h1>
          <Button asChild>
            <Link to="/">
              Back home
              <ArrowRight className="w-3.5 h-3.5 ml-2" />
            </Link>
          </Button>
        </div>
      </Section>
    </Page>
  );
}
