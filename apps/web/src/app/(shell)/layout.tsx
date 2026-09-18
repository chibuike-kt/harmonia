import type { ReactNode } from "react";
import { Sidebar } from "@/components/Sidebar";

// Persistent authenticated shell — sidebar + outlet — for every route
// nested under this (shell) route group (currently /dashboard and
// /rooms/[id]; see docs/design/dashboard-build-brief.md). A route group
// folder doesn't appear in the URL, so this changes nothing about either
// route's path.
//
// Rendering Sidebar once here, above {children}, is what keeps it
// mounted (collapse/width/rooms state intact) across navigation within
// this group — Next.js only remounts the parts of the tree that actually
// change between two routes sharing a layout, not the layout itself.
//
// The --font-* variables font-[family-name:...] resolves through are
// defined globally on <html> in the root layout, not re-declared here.
export default function ShellLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-screen w-full overflow-hidden bg-[var(--login-bg)] text-[var(--login-text)] font-[family-name:var(--login-font-sans)]">
      <Sidebar />
      {/* A plain div, not <main>: each page under this layout (dashboard,
          room view, the IDE) already renders its own single <main>
          landmark. This outlet is deliberately not a scroll container
          itself (overflow-hidden, not overflow-y-auto) — the outer shell
          never scrolls as a whole; every page under it owns its own
          internal scroll regions instead. min-h-0 is what actually makes
          that real: a flex child defaults to min-height/min-width auto
          (never smaller than its content), so without it a page whose
          content ran even slightly tall would silently push this outlet
          past 100% height instead of clipping — the exact bug this
          fixes: a real, visible whole-page scrollbar stacked on top of
          whatever internal panel (a room's transcript, the IDE's
          terminal) already scrolls on its own. */}
      <div className="min-h-0 min-w-0 flex-1 overflow-hidden">{children}</div>
    </div>
  );
}
