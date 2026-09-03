import { Link } from "react-router";
import { ArrowRight } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { Button } from "../components/ui/button";

export default function NotFound() {
  return (
    <Page title="Not Found">
      <Section padding="wide">
        <div className="flex flex-col items-center text-center gap-6 py-24">
          <span className="font-mono text-xs tracking-widest uppercase text-muted">
            404
          </span>
          <h1 className="font-display font-bold text-3xl md:text-4xl text-fg tracking-tight">
            This page doesn't exist.
          </h1>
          <p className="text-sm text-muted max-w-md leading-relaxed">
            This site is a landing page and the documentation, nothing else.
            Whatever you were after is most likely in the docs.
          </p>
          <div className="flex flex-wrap gap-3 justify-center pt-2">
            <Button asChild>
              <Link to="/docs">
                Read the docs
                <ArrowRight className="w-3.5 h-3.5 ml-2" />
              </Link>
            </Button>
            <Button asChild variant="ghost">
              <Link to="/">Back home</Link>
            </Button>
          </div>
        </div>
      </Section>
    </Page>
  );
}
