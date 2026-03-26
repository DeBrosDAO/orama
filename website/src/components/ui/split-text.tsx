import { useRef, useEffect, useCallback } from "react";
import gsap from "gsap";

export interface SplitTextProps {
  text: string;
  className?: string;
  delay?: number;
  duration?: number;
  ease?: string;
  splitType?: "chars" | "words";
  from?: gsap.TweenVars;
  to?: gsap.TweenVars;
  threshold?: number;
  rootMargin?: string;
}

export function SplitText({
  text,
  className = "",
  delay = 50,
  duration = 0.8,
  ease = "power3.out",
  splitType = "chars",
  from = { opacity: 0, y: 40 },
  to = { opacity: 1, y: 0 },
  threshold = 0.1,
  rootMargin = "-50px",
}: SplitTextProps) {
  const containerRef = useRef<HTMLSpanElement>(null);
  const hasAnimated = useRef(false);

  const getElements = useCallback(() => {
    if (!containerRef.current) return [];
    return Array.from(
      containerRef.current.querySelectorAll(
        splitType === "chars" ? ".split-char" : ".split-word",
      ),
    );
  }, [splitType]);

  useEffect(() => {
    const elements = getElements();
    if (elements.length === 0) return;

    // Set initial state
    gsap.set(elements, from);

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting && !hasAnimated.current) {
          hasAnimated.current = true;

          gsap.to(elements, {
            ...to,
            duration,
            ease,
            stagger: delay / 1000,
          });

          observer.disconnect();
        }
      },
      { threshold, rootMargin },
    );

    if (containerRef.current) {
      observer.observe(containerRef.current);
    }

    return () => observer.disconnect();
  }, [getElements, from, to, duration, ease, delay, threshold, rootMargin]);

  const renderContent = () => {
    if (splitType === "words") {
      return text.split(" ").map((word, i) => (
        <span key={i} className="split-word inline-block" style={{ opacity: 0 }}>
          {word}
          {i < text.split(" ").length - 1 ? "\u00A0" : ""}
        </span>
      ));
    }

    // chars — split by words first, then chars within each word
    return text.split(" ").map((word, wi) => (
      <span key={wi} className="inline-block whitespace-nowrap">
        {word.split("").map((char, ci) => (
          <span
            key={`${wi}-${ci}`}
            className="split-char inline-block"
            style={{ opacity: 0 }}
          >
            {char}
          </span>
        ))}
        {wi < text.split(" ").length - 1 && (
          <span className="split-char inline-block" style={{ opacity: 0 }}>
            {"\u00A0"}
          </span>
        )}
      </span>
    ));
  };

  return (
    <span ref={containerRef} className={className}>
      {renderContent()}
    </span>
  );
}
