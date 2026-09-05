import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Harmonia",
  description: "Coordinate real handoffs between AI agents.",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className="h-full">
      <body className="min-h-full flex flex-col">{children}</body>
    </html>
  );
}
