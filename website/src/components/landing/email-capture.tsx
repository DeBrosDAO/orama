import { useState } from "react";
import { Section } from "../layout/section";
import { DashedPanel } from "../ui/dashed-panel";
import { Button } from "../ui/button";
import { AnimateIn } from "../ui/animate-in";
import { ArrowRight } from "lucide-react";

export function EmailCapture({
  heading = "Get early access & updates.",
  description = "Be the first to know when public node onboarding, the token launch, and mainnet go live.",
}: {
  heading?: string;
  description?: string;
}) {
  const [email, setEmail] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [error, setError] = useState("");

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");

    if (!email || !email.includes("@") || !email.includes(".")) {
      setError("Please enter a valid email address.");
      return;
    }

    // TODO: Replace with actual API endpoint
    console.log("Email captured:", email);
    setSubmitted(true);
  }

  return (
    <Section>
      <AnimateIn>
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col items-center text-center gap-5 py-6">
            <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
              {heading}
            </h2>
            <p className="text-muted max-w-lg leading-relaxed">
              {description}
            </p>

            {submitted ? (
              <div className="flex items-center gap-2 text-accent-2 font-mono text-sm tracking-wider uppercase">
                <span className="w-2 h-2 rounded-full bg-accent-2 animate-pulse-dot" />
                You're on the list
              </div>
            ) : (
              <form
                onSubmit={handleSubmit}
                className="flex flex-col sm:flex-row items-center gap-3 w-full max-w-md"
              >
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="your@email.com"
                  className="w-full flex-1 px-4 py-3 bg-surface-2 border border-border text-fg text-sm font-mono placeholder:text-muted/50 focus:outline-none focus:border-accent/50 transition-colors rounded-sm"
                />
                <Button type="submit" size="default" className="shrink-0 w-full sm:w-auto">
                  Subscribe
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </Button>
              </form>
            )}

            {error && (
              <p className="text-red-400 text-xs font-mono">{error}</p>
            )}

            <p className="text-muted/50 text-xs font-mono">
              No spam. Unsubscribe anytime.
            </p>
          </div>
        </DashedPanel>
      </AnimateIn>
    </Section>
  );
}
