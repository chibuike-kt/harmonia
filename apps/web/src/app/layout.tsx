import type { Metadata } from "next";
import type { ReactNode } from "react";
import "./globals.css";
import { jetbrainsMono, pirataOne, spaceGrotesk } from "@/lib/fonts";

export const metadata: Metadata = {
  title: "Harmonia",
  description: "Coordinate real handoffs between AI agents.",
};

// The three .variable classes below only *define* --font-space-grotesk /
// --font-jetbrains-mono / --font-pirata-one as CSS custom properties —
// they don't apply any font by themselves, so putting them on <body>
// doesn't change anything for pages that don't reference those
// variables (the plain Arial-body pages stay exactly as they are).
// Actually rendering a page in one of these fonts is still each page's
// own explicit opt-in via font-[family-name:var(--login-font-sans)] (or
// --font-pirata-one directly, for Nav's wordmark) — see login/page.tsx
// and (shell)/layout.tsx.
//
// Originally these lived on login/page.tsx's own wrapper div instead of
// here, on the theory that only that one page needed them. That broke
// the moment a second page (the dashboard) needed the same fonts: having
// every consumer re-declare all three .variable classes is exactly the
// kind of thing that's easy to forget, and did get forgotten. Defining
// them once, globally, removes that whole footgun.
export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html
      lang="en"
      className={`h-full ${spaceGrotesk.variable} ${jetbrainsMono.variable} ${pirataOne.variable}`}
    >
      <body className="min-h-full flex flex-col">{children}</body>
    </html>
  );
}
