"use client";

import { useEffect, useState } from "react";

// Matches Tailwind's own `md` breakpoint exactly (768px) — every
// component that branches on this hook also uses plain `md:` utility
// classes for the parts of its mobile treatment that are pure CSS (e.g.
// hiding a resize handle). Picking any other pixel value here would let
// the JS-driven branch (drawer vs. persistent sidebar, full-screen vs.
// side panel, drill-down vs. two-column settings) and the CSS-driven
// classes disagree at some width in between, producing a broken hybrid
// layout neither treatment was designed for.
const MOBILE_BREAKPOINT = 768;

/**
 * Reports whether the viewport is currently narrower than the mobile
 * breakpoint — a real `matchMedia` evaluation, not a `resize` event
 * listener: `resize` doesn't fire for an iframe's fixed CSS viewport at
 * creation (nothing "resizes" — it's sized once, from the start), so a
 * component gated on a resize-driven flag would render the desktop
 * layout inside a phone-width iframe with no event left to correct it.
 * `matchMedia` evaluates against the actual current viewport immediately
 * on mount, and its own `change` listener (not `window`'s `resize`)
 * fires correctly for a real viewport change too.
 *
 * Starts `false` (desktop) on the server/first client render — SSR has
 * no viewport to measure — and corrects itself in the effect below,
 * same "measure after mount" posture as useIsTruncated's own ResizeObserver.
 */
export function useIsMobile(): boolean {
  const [isMobile, setIsMobile] = useState(false);

  useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`);
    const update = () => setIsMobile(mql.matches);
    update();
    mql.addEventListener("change", update);
    return () => mql.removeEventListener("change", update);
  }, []);

  return isMobile;
}
