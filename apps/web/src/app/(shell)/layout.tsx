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
          room view) already renders its own single <main> landmark —
          this is just the scrollable outlet around it. */}
      <div className="min-w-0 flex-1 overflow-y-auto">{children}</div>
    </div>
  );
}
