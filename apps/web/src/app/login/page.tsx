import { apiUrl } from "@/lib/api";

// Plain anchors, not next/link: these are full-page navigations to the
// Go backend's own redirect (GET /v1/auth/{provider}/login), which then
// 302s on to the real provider — not client-side routing within this
// app, so next/link's prefetch/soft-navigation behavior doesn't apply.
export default function LoginPage() {
  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-6 p-8 text-center">
      <h1 className="text-2xl font-semibold">Log in to Harmonia</h1>
      <div className="flex w-full max-w-xs flex-col gap-3">
        <a
          href={apiUrl("/v1/auth/github/login")}
          className="rounded-md border border-foreground/20 px-4 py-2 font-medium transition-colors hover:bg-foreground/5"
        >
          Continue with GitHub
        </a>
        <a
          href={apiUrl("/v1/auth/google/login")}
          className="rounded-md border border-foreground/20 px-4 py-2 font-medium transition-colors hover:bg-foreground/5"
        >
          Continue with Google
        </a>
      </div>
    </main>
  );
}
