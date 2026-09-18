"use client";

import type { ReactNode } from "react";

interface TooltipProps {
  label: string;
  children: ReactNode;
  /** Never render the tooltip — used for a room name that isn't actually truncated. */
  disabled?: boolean;
  className?: string;
  /** For a label too long to read as one nowrap line (an explanatory
   *  sentence, not a short action name) — wraps to a fixed-width block
   *  instead of stretching off-screen. */
  wrap?: boolean;
  /** Which side of the trigger the label opens on. Default "top" (the
   *  original behavior). Pick "bottom" for a trigger near the top edge
   *  of the viewport, "right" for one hugging the left edge (a vertical
   *  icon rail), "left" for one hugging the right edge — whichever
   *  direction actually has room, rather than always opening upward and
   *  letting it clip off-screen. */
  side?: "top" | "bottom" | "left" | "right";
  /** Horizontal anchor for a top/bottom tooltip: "center" (default) or
   *  "end" — end anchors the tooltip's right edge to the trigger's right
   *  edge instead of centering, for a trigger close to the right side of
   *  the viewport where a centered label would overflow past it. */
  align?: "center" | "end";
}

const SIDE_CLASSES: Record<
  NonNullable<TooltipProps["side"]>,
  Record<NonNullable<TooltipProps["align"]>, string>
> = {
  top: {
    center: "bottom-[calc(100%+6px)] left-1/2 -translate-x-1/2",
    end: "bottom-[calc(100%+6px)] right-0",
  },
  bottom: {
    center: "top-[calc(100%+6px)] left-1/2 -translate-x-1/2",
    end: "top-[calc(100%+6px)] right-0",
  },
  right: {
    center: "left-[calc(100%+6px)] top-1/2 -translate-y-1/2",
    end: "left-[calc(100%+6px)] top-1/2 -translate-y-1/2",
  },
  left: {
    center: "right-[calc(100%+6px)] top-1/2 -translate-y-1/2",
    end: "right-[calc(100%+6px)] top-1/2 -translate-y-1/2",
  },
};

// CSS-only show/hide (group-hover), same philosophy as the rest of this
// design system's interactions (Nav's mega-menus, the sidebar's own
// hover-revealed controls) — no JS state needed just to show a label on
// hover. `disabled` fully omits the tooltip markup rather than hiding it,
// for the truncated-room-name case where most rows shouldn't have one at all.
export function Tooltip({
  label,
  children,
  disabled,
  className,
  wrap,
  side = "top",
  align = "center",
}: TooltipProps) {
  if (disabled) return <>{children}</>;

  return (
    <span className={`group/tooltip relative inline-flex ${className ?? ""}`}>
      {children}
      <span
        role="tooltip"
        className={`pointer-events-none absolute z-50 rounded-md border border-[var(--login-border-strong)] bg-[var(--login-surface)] px-2 py-1 text-[12px] text-[var(--login-text)] opacity-0 shadow-[0_4px_16px_rgba(0,0,0,0.4)] transition-opacity delay-300 duration-100 group-hover/tooltip:opacity-100 ${SIDE_CLASSES[side][align]} ${
          wrap
            ? "w-64 whitespace-normal text-left leading-[1.4]"
            : "whitespace-nowrap"
        }`}
      >
        {label}
      </span>
    </span>
  );
}
