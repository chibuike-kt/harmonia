import Link from "next/link";

export default function Home() {
  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-4 p-8 text-center">
      <h1 className="text-2xl font-semibold">Harmonia</h1>
      <p className="text-foreground/70 max-w-md">
        Real-time coordination for AI agent handoffs. Room and credential
        screens land in the next steps.
      </p>
      <Link
        href="/login"
        className="rounded-md border border-foreground/20 px-4 py-2 font-medium transition-colors hover:bg-foreground/5"
      >
        Log in
      </Link>
    </main>
  );
}
