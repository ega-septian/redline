import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

type Align = "start" | "center" | "end";

/**
 * A small, dependency-free tooltip for filter controls. It opens on hover and
 * on keyboard focus, and stays out of the pointer path so it never blocks the
 * control underneath.
 */
export function Tooltip({
  content,
  children,
  align = "center",
  mobileAlign,
  className,
}: {
  content: ReactNode;
  children: ReactNode;
  align?: Align;
  mobileAlign?: Align;
  className?: string;
}) {
  return (
    <span className={cn("group relative inline-flex", className)}>
      {children}
      <span
        role="tooltip"
        className={cn(
          "pointer-events-none invisible absolute bottom-full z-50 mb-2 w-60 max-w-[calc(100vw-2rem)] rounded-lg border border-[#3d444d] bg-[#1c2129] px-3 py-2 text-[11.5px] leading-relaxed text-[#e6edf3] opacity-0 shadow-[0_12px_32px_#00000080] transition-opacity duration-150 group-hover:visible group-hover:opacity-100 group-focus-within:visible group-focus-within:opacity-100 max-sm:w-52",
          align === "start" && "left-0",
          align === "center" && "left-1/2 -translate-x-1/2",
          align === "end" && "right-0",
          mobileAlign === "start" && "max-sm:left-0 max-sm:right-auto max-sm:translate-x-0",
          mobileAlign === "center" &&
            "max-sm:left-1/2 max-sm:right-auto max-sm:-translate-x-1/2",
          mobileAlign === "end" && "max-sm:left-auto max-sm:right-0 max-sm:translate-x-0",
        )}
      >
        {content}
        <svg
          aria-hidden="true"
          width="16"
          height="8"
          viewBox="0 0 16 8"
          className={cn(
            "absolute top-full",
            align === "start" && "left-2",
            align === "center" && "left-1/2 -translate-x-1/2",
            align === "end" && "right-2",
            mobileAlign === "start" && "max-sm:left-2 max-sm:right-auto max-sm:translate-x-0",
            mobileAlign === "center" &&
              "max-sm:left-1/2 max-sm:right-auto max-sm:-translate-x-1/2",
            mobileAlign === "end" && "max-sm:left-auto max-sm:right-2 max-sm:translate-x-0",
          )}
        >
          <path
            d="M1 0.5L8 7.5L15 0.5"
            fill="#1c2129"
            stroke="#3d444d"
            strokeLinejoin="round"
          />
        </svg>
      </span>
    </span>
  );
}
