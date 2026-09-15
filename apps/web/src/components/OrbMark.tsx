"use client";

import { useEffect, useRef } from "react";

/**
 * Harmonia's own mark — a small cluster of orbiting dots, not a generic
 * icon. Ported directly from docs/design/harmonia-ide-mockup.html's own
 * drawOrb: a canvas animation, not a static SVG, since the mockup's own
 * "real" empty state and brand mark are both this exact live animation,
 * not a frozen frame of it. Used at several sizes across the IDE (the
 * menu bar's brand mark, the command palette's input, the empty state's
 * large centered orb, and — small and per-agent — provider tag glyphs
 * that want Harmonia's own liveness language rather than a logo).
 */
export function OrbMark({
  size,
  dotCount = 7,
  radius,
  dotSize,
}: {
  size: number;
  dotCount?: number;
  radius?: number;
  dotSize?: number;
}) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const r = radius ?? size * 0.42;
    const dot = dotSize ?? Math.max(0.9, size * 0.09);
    const cx = size / 2;
    const cy = size / 2;
    const dots = Array.from({ length: dotCount }, (_, i) => ({
      angle: (i / dotCount) * Math.PI * 2,
      tilt: 0.4 + (i % 3) * 0.15,
      speed: 0.02 + (i % 4) * 0.006,
      r: r * (0.6 + (i % 3) * 0.2),
    }));

    let frameId: number;
    function frame() {
      if (!ctx) return;
      ctx.clearRect(0, 0, size, size);
      for (const d of dots) {
        d.angle += d.speed;
        const x = cx + Math.cos(d.angle) * d.r;
        const y = cy + Math.sin(d.angle) * d.r * d.tilt;
        const depth = (Math.sin(d.angle) * d.tilt + 1) / 2;
        ctx.beginPath();
        ctx.arc(x, y, dot * (0.5 + depth * 0.5), 0, Math.PI * 2);
        ctx.fillStyle = `rgba(76, 211, 194, ${0.4 + depth * 0.55})`;
        ctx.fill();
      }
      frameId = requestAnimationFrame(frame);
    }
    frameId = requestAnimationFrame(frame);
    return () => cancelAnimationFrame(frameId);
  }, [size, dotCount, radius, dotSize]);

  return (
    <canvas
      ref={canvasRef}
      width={size}
      height={size}
      aria-hidden="true"
      className="shrink-0"
    />
  );
}
