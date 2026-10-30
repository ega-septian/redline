import type * as React from "react";

import { cn } from "@/lib/utils";

function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      type={type}
      className={cn(
        "h-8 w-full rounded-md border border-line bg-canvas px-2.5 text-xs text-ink outline-none placeholder:text-faint focus-visible:border-brand/60 focus-visible:ring-2 focus-visible:ring-brand/20",
        className,
      )}
      {...props}
    />
  );
}

export { Input };
