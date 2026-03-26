import { useState } from "react";
import { Section } from "../layout/section";
import { SectionHeader } from "../ui/section-header";
import { DashedPanel } from "../ui/dashed-panel";
import { Button } from "../ui/button";
import { AnimateIn } from "../ui/animate-in";
import { ArrowRight } from "lucide-react";

const WEB3FORMS_ACCESS_KEY = "YOUR_ACCESS_KEY_HERE";

const inputClass =
  "w-full px-4 py-3 bg-surface-2 border border-border text-fg text-sm font-mono placeholder:text-muted/50 focus:outline-none focus:border-accent/50 transition-colors rounded-sm";

export function InvestorForm() {
  const [submitted, setSubmitted] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError("");
    setLoading(true);

    const formData = new FormData(e.currentTarget);
    formData.append("access_key", WEB3FORMS_ACCESS_KEY);
    formData.append("subject", "New Investor Inquiry — Orama Network");
    formData.append("from_name", "Orama Network Website");

    try {
      const res = await fetch("https://api.web3forms.com/submit", {
        method: "POST",
        body: formData,
      });
      const data = await res.json();
      if (data.success) {
        setSubmitted(true);
      } else {
        setError("Something went wrong. Please try again.");
      }
    } catch {
      setError("Network error. Please try again.");
    } finally {
      setLoading(false);
    }
  }

  return (
    <Section>
      <AnimateIn>
        <SectionHeader
          title="Get in Touch"
          subtitle="Interested in backing Orama Network? We'd love to hear from you."
        />

        <div className="mt-8">
          <DashedPanel withCorners withBackground>
            {submitted ? (
              <div className="flex flex-col items-center text-center gap-4 py-8">
                <div className="flex items-center gap-2 text-accent-2 font-mono text-sm tracking-wider uppercase">
                  <span className="w-2 h-2 rounded-full bg-accent-2 animate-pulse-dot" />
                  Message Received
                </div>
                <p className="text-muted max-w-md">
                  Thanks for reaching out. We'll get back to you shortly.
                </p>
              </div>
            ) : (
              <form onSubmit={handleSubmit} className="flex flex-col gap-4">
                <input type="hidden" name="to" value="dev@debros.io" />
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                  <input
                    type="text"
                    name="name"
                    placeholder="Name *"
                    required
                    className={inputClass}
                  />
                  <input
                    type="email"
                    name="email"
                    placeholder="Email *"
                    required
                    className={inputClass}
                  />
                </div>
                <input
                  type="text"
                  name="role"
                  placeholder="Role (e.g. VC Partner, Angel, Builder)"
                  className={inputClass}
                />
                <textarea
                  name="message"
                  placeholder="Tell us about your interest in Orama Network..."
                  rows={4}
                  className={`${inputClass} resize-none`}
                />

                {error && (
                  <p className="text-red-400 text-xs font-mono">{error}</p>
                )}

                <Button type="submit" size="lg" className="w-full sm:w-auto self-end" disabled={loading}>
                  {loading ? "Sending..." : "Send Message"}
                  {!loading && <ArrowRight className="w-3.5 h-3.5 ml-2" />}
                </Button>
              </form>
            )}
          </DashedPanel>
        </div>
      </AnimateIn>
    </Section>
  );
}
