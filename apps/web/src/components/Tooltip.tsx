"use client";

import type { ReactNode } from "react";

interface TooltipProps {
  label: string;
  children: ReactNode;
  /** Never render the tooltip — used for a room name that isn't actually truncated. */
  disabled?: boolean;
  className?: string;
}

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
}: TooltipProps) {
  if (disabled) return <>{children}</>;

  return (
    <span className={`group/tooltip relative inline-flex ${className ?? ""}`}>
      {children}
      <span
        role="tooltip"
        className="pointer-events-none absolute bottom-[calc(100%+6px)] left-1/2 z-50 -translate-x-1/2 whitespace-nowrap rounded-md border border-[var(--login-border-strong)] bg-[var(--login-surface)] px-2 py-1 text-[12px] text-[var(--login-text)] opacity-0 shadow-[0_4px_16px_rgba(0,0,0,0.4)] transition-opacity delay-300 duration-100 group-hover/tooltip:opacity-100"
      >
        {label}
      </span>
    </span>
  );
}
