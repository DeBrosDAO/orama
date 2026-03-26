import { forwardRef } from "react";
import type { ButtonHTMLAttributes } from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva } from "class-variance-authority";
import type { VariantProps } from "class-variance-authority";
import { cn } from "../../lib/utils";

export const buttonVariants = cva(
  "inline-flex items-center justify-center font-mono font-semibold tracking-wider uppercase transition-all duration-200 ease-out focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:ring-offset-2 focus-visible:ring-offset-bg disabled:pointer-events-none disabled:opacity-50 cursor-pointer",
  {
    variants: {
      variant: {
        primary:
          "bg-accent text-black relative border border-white/[0.08] border-t-white/20 shadow-[0_1px_2px_rgba(0,0,0,0.4),inset_0_1px_0_rgba(255,255,255,0.08)] hover:shadow-[0_0_20px_rgba(161,161,170,0.35),0_1px_3px_rgba(0,0,0,0.4),inset_0_1px_0_rgba(255,255,255,0.1)] hover:bg-accent/95 active:shadow-[inset_0_1px_3px_rgba(0,0,0,0.3)] active:translate-y-px",
        ghost:
          "border border-border/60 text-muted hover:border-fg/30 hover:text-fg hover:bg-white/[0.03] active:bg-white/[0.05]",
        dashed:
          "border border-dashed border-accent/60 text-accent hover:border-accent hover:bg-accent/[0.08] hover:shadow-[0_0_12px_rgba(161,161,170,0.15)] active:bg-accent/[0.12]",
        link: "text-accent hover:text-accent/80 underline-offset-4 hover:underline",
      },
      size: {
        sm: "px-3.5 py-1.5 text-xs rounded-sm",
        default: "px-5 py-2.5 text-xs rounded-sm",
        lg: "px-8 py-3 text-sm rounded-sm",
      },
    },
    defaultVariants: {
      variant: "primary",
      size: "default",
    },
  },
);

export interface ButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";

    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        ref={ref}
        {...props}
      />
    );
  },
);

Button.displayName = "Button";
