import { type ComponentPropsWithoutRef, Children } from "react";
import type { MDXComponents } from "mdx/types";
import { cn } from "../lib/utils";
import { MdxPre } from "./ui/code-block-mdx";

function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/(^-|-$)/g, "");
}

function extractText(children: React.ReactNode): string {
  let text = "";
  Children.forEach(children, (child) => {
    if (typeof child === "string") text += child;
    else if (typeof child === "number") text += String(child);
    else if (child && typeof child === "object" && "props" in child) {
      const props = child.props as { children?: React.ReactNode };
      text += extractText(props.children);
    }
  });
  return text;
}

export const mdxComponents: MDXComponents = {
  h1: (props: ComponentPropsWithoutRef<"h1">) => (
    <h1
      className="font-display text-3xl sm:text-4xl font-bold text-fg mb-6 mt-8 first:mt-0"
      {...props}
    />
  ),

  h2: ({ children, ...props }: ComponentPropsWithoutRef<"h2">) => {
    const id = slugify(extractText(children));
    return (
      <h2
        id={id}
        className="font-display text-2xl font-bold text-fg mb-4 mt-10 pb-2 border-b border-dashed border-border"
        {...props}
      >
        {children}
      </h2>
    );
  },

  h3: ({ children, ...props }: ComponentPropsWithoutRef<"h3">) => {
    const id = slugify(extractText(children));
    return (
      <h3
        id={id}
        className="font-display text-xl font-semibold text-fg mb-3 mt-8"
        {...props}
      >
        {children}
      </h3>
    );
  },

  h4: (props: ComponentPropsWithoutRef<"h4">) => (
    <h4
      className="font-display text-lg font-semibold text-fg mb-2 mt-6"
      {...props}
    />
  ),

  p: (props: ComponentPropsWithoutRef<"p">) => (
    <p className="text-base text-muted leading-relaxed mb-4" {...props} />
  ),

  a: (props: ComponentPropsWithoutRef<"a">) => (
    <a
      className="text-accent hover:underline transition-colors"
      target={props.href?.startsWith("http") ? "_blank" : undefined}
      rel={props.href?.startsWith("http") ? "noopener noreferrer" : undefined}
      {...props}
    />
  ),

  code: (props: ComponentPropsWithoutRef<"code">) => {
    const isInline = !props.className?.includes("language-");
    if (isInline) {
      return (
        <code
          className="font-mono text-sm bg-surface-2 px-1.5 py-0.5 rounded text-accent"
          {...props}
        />
      );
    }
    return <code {...props} />;
  },

  pre: MdxPre,

  ul: ({ className, ...props }: ComponentPropsWithoutRef<"ul">) => (
    <ul className={cn("mb-4 space-y-1", className)} {...props} />
  ),

  li: ({ children, ...props }: ComponentPropsWithoutRef<"li">) => (
    <li className="text-muted leading-relaxed flex gap-2" {...props}>
      <span className="text-accent font-mono text-sm select-none shrink-0 mt-0.5">
        +
      </span>
      <span>{children}</span>
    </li>
  ),

  ol: ({ className, ...props }: ComponentPropsWithoutRef<"ol">) => (
    <ol
      className={cn("mb-4 space-y-1 list-none counter-reset-item", className)}
      style={{ counterReset: "item" }}
      {...props}
    />
  ),

  blockquote: (props: ComponentPropsWithoutRef<"blockquote">) => (
    <blockquote
      className="border-l-2 border-accent pl-4 my-4 text-muted italic"
      {...props}
    />
  ),

  table: (props: ComponentPropsWithoutRef<"table">) => (
    <div className="overflow-x-auto mb-4">
      <table className="w-full border-collapse" {...props} />
    </div>
  ),

  thead: (props: ComponentPropsWithoutRef<"thead">) => (
    <thead className="border-b border-dashed border-border" {...props} />
  ),

  th: (props: ComponentPropsWithoutRef<"th">) => (
    <th
      className="text-left font-mono text-xs tracking-wider uppercase text-muted px-3 py-2"
      {...props}
    />
  ),

  td: (props: ComponentPropsWithoutRef<"td">) => (
    <td
      className="text-sm text-muted px-3 py-2 border-b border-dashed border-border"
      {...props}
    />
  ),

  tr: (props: ComponentPropsWithoutRef<"tr">) => (
    <tr className="hover:bg-surface-2/50 transition-colors" {...props} />
  ),

  hr: () => <hr className="border-t border-dashed border-border my-8" />,

  strong: (props: ComponentPropsWithoutRef<"strong">) => (
    <strong className="text-fg font-semibold" {...props} />
  ),

  em: (props: ComponentPropsWithoutRef<"em">) => (
    <em className="text-muted italic" {...props} />
  ),

  img: (props: ComponentPropsWithoutRef<"img">) => (
    <img
      className="max-w-full rounded border border-dashed border-border my-4"
      loading="lazy"
      {...props}
    />
  ),
};
