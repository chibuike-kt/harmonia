import { Space_Grotesk, JetBrains_Mono, Pirata_One } from "next/font/google";

// Scoped via .variable, not applied to <body> in the root layout: these
// back the marketing/login design system's --font-sans/--font-mono
// (see globals.css), not the app's default typography. Attach both
// .variable classes to a container to opt that subtree in.
export const spaceGrotesk = Space_Grotesk({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  variable: "--font-space-grotesk",
});

export const jetbrainsMono = JetBrains_Mono({
  subsets: ["latin"],
  weight: ["400", "500"],
  variable: "--font-jetbrains-mono",
});

// "Pirata One" (confirmed via next/font/google's own type declarations —
// exported as Pirata_One, family "Pirata One") — the "Harmonia" wordmark
// only, never body/subhead copy. Ships exactly one weight (400); next's
// types make `weight` required here rather than defaulting, since there's
// nothing to default to.
export const pirataOne = Pirata_One({
  subsets: ["latin"],
  weight: ["400"],
  variable: "--font-pirata-one",
});
