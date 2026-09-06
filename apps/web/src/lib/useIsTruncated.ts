"use client";

import { useEffect, useRef, useState } from "react";

/**
 * Reports whether the element it's attached to is actually clipped by
 * text-ellipsis truncation right now — not "might be," a real per-render
 * measurement (scrollWidth vs. clientWidth). Re-checked via
 * ResizeObserver, not just on mount: the sidebar this is built for is
 * itself resizable, so a room name's truncated state can change as the
 * user drags the edge handle.
 *
 * `watch` covers the case ResizeObserver can't: the element's own box
 * (fixed at w-full) doesn't resize when only its *text* changes, e.g. a
 * rename, so nothing would otherwise trigger a recheck. Pass the text
 * content itself so a change re-runs the measurement.
 */
export function useIsTruncated<T extends HTMLElement>(watch?: unknown) {
  const ref = useRef<T>(null);
  const [truncated, setTruncated] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const check = () => setTruncated(el.scrollWidth > el.clientWidth);
    check();

    const observer = new ResizeObserver(check);
    observer.observe(el);
    return () => observer.disconnect();
  }, [watch]);

  return { ref, truncated };
}
