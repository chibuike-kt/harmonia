"use client";

import {
  useCallback,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
} from "react";

// The exact same real drag-resize mechanism Harmonia's own main Sidebar
// has used for months (Sidebar.tsx's handleResizeStart) — an edge handle
// whose onMouseDown starts a real document-level mousemove/mouseup pair,
// clamped to [min, max], reused here instead of a second implementation
// so the IDE's Explorer and Terminal panels resize exactly the way every
// other resizable surface in this product already does.
export function useResizable({
  axis,
  initial,
  min,
  max,
  // "row-reverse": dragging right shrinks (used for a panel resized from
  // its *left* edge, like the Explorer's own right-hand handle sitting
  // between it and the editor). Plain "row"/"column": dragging
  // right/down grows (the Terminal panel's own top-edge handle, where
  // dragging up — a shrinking clientY — must grow its height).
  direction = "normal",
}: {
  axis: "x" | "y";
  initial: number;
  min: number;
  max: number;
  direction?: "normal" | "reverse";
}) {
  const [size, setSize] = useState(initial);
  const [resizing, setResizing] = useState(false);
  const sizeAtDragStart = useRef(initial);
  const posAtDragStart = useRef(0);

  const onHandleMouseDown = useCallback(
    (event: ReactMouseEvent) => {
      event.preventDefault();
      setResizing(true);
      sizeAtDragStart.current = size;
      posAtDragStart.current = axis === "x" ? event.clientX : event.clientY;

      const onMove = (moveEvent: globalThis.MouseEvent) => {
        const pos = axis === "x" ? moveEvent.clientX : moveEvent.clientY;
        const delta = pos - posAtDragStart.current;
        const signed = direction === "reverse" ? -delta : delta;
        setSize(Math.min(max, Math.max(min, sizeAtDragStart.current + signed)));
      };
      const onUp = () => {
        setResizing(false);
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
      };
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
    },
    [axis, direction, max, min, size],
  );

  return { size, setSize, resizing, onHandleMouseDown };
}
