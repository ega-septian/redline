import { cva, type VariantProps } from "class-variance-authority";
import type * as React from "react";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[10px] font-bold tracking-wider whitespace-nowrap",
  {
    variants: {
      tone: {
        neutral: "border-line bg-canvas text-muted",
        brand: "border-brand/40 bg-brand/10 text-brand",
        backend: "border-danger/40 bg-danger/10 text-danger",
        test: "border-violet/40 bg-violet/10 text-violet",
        env: "border-warn/40 bg-warn/10 text-warn",
        flaky: "border-ok/40 bg-ok/10 text-ok",
      },
    },
    defaultVariants: {
      tone: "neutral",
    },
  },
);

function Badge({
  className,
  tone,
  ...props
}: React.ComponentProps<"span"> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ tone }), className)} {...props} />;
}

export { Badge, badgeVariants };
