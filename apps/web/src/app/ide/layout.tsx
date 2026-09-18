import type { ReactNode } from "react";

// The IDE is deliberately its own focused surface, not nested under
// (shell)'s layout — no real IDE shows a separate product's own
// navigation chrome (Harmonia's Dashboard/Rooms/Agents/Artifacts
// sidebar) alongside its own UI, and neither should this one. This
// layout's only job is the same real, non-scrolling viewport boundary
// (shell)/layout.tsx gives its own pages — h-screen + overflow-hidden —
// so the IDE page underneath can own 100% of it for its own activity
// bar, editor, and terminal, exactly the way (shell)'s own pages own
// their outlet.
export default function IdeLayout({ children }: { children: ReactNode }) {
  return <div className="h-screen w-full overflow-hidden">{children}</div>;
}
