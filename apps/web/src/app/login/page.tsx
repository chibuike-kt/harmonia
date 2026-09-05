import { Footer } from "@/components/Footer";
import { Nav } from "@/components/Nav";
import { GitHubIcon, GoogleIcon } from "@/components/icons";
import { apiUrl } from "@/lib/api";
import { jetbrainsMono, pirataOne, spaceGrotesk } from "@/lib/fonts";

// Plain anchors, not next/link: these are full-page navigations to the
// Go backend's own redirect (GET /v1/auth/{provider}/login), which then
// 302s on to the real provider — not client-side routing within this
// app, so next/link's prefetch/soft-navigation behavior doesn't apply.
export default function LoginPage() {
  return (
    <div
      className={`${spaceGrotesk.variable} ${jetbrainsMono.variable} ${pirataOne.variable} flex min-h-screen flex-col bg-[var(--login-bg)] text-[var(--login-text)] font-[family-name:var(--login-font-sans)]`}
    >
      <Nav />

      <main className="flex flex-1 flex-col items-center justify-center px-5 py-16 sm:px-8">
        <div className="mb-10 max-w-xl text-center">
          <h1 className="mb-4 text-[34px] leading-[1.15] font-medium tracking-[-0.015em] sm:text-[42px] md:text-[58px]">
            Coordinate what&apos;s next
          </h1>
          <p className="text-lg text-[var(--login-text-secondary)] sm:text-[19px]">
            Agents that work together on your behalf
          </p>
        </div>

        <div className="w-full max-w-[420px] rounded-xl border border-[var(--login-border)] bg-[var(--login-surface)] px-8 pb-7 pt-8">
          <div className="flex flex-col gap-2.5">
            <a
              href={apiUrl("/v1/auth/github/login")}
              className="flex h-12 items-center justify-center gap-2.5 rounded-lg border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] text-[15px] font-medium transition-colors hover:border-[#3A4453] hover:bg-[#1C222B] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--login-accent)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--login-bg)]"
            >
              <GitHubIcon />
              Continue with GitHub
            </a>

            <div className="my-1 flex items-center gap-3 text-xs text-[var(--login-text-muted)]">
              <span className="h-px flex-1 bg-[var(--login-border)]" />
              OR
              <span className="h-px flex-1 bg-[var(--login-border)]" />
            </div>

            <a
              href={apiUrl("/v1/auth/google/login")}
              className="flex h-12 items-center justify-center gap-2.5 rounded-lg border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] text-[15px] font-medium transition-colors hover:border-[#3A4453] hover:bg-[#1C222B] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--login-accent)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--login-bg)]"
            >
              <GoogleIcon />
              Continue with Google
            </a>
          </div>

          <p className="mt-4 text-center text-[13px] leading-relaxed text-[var(--login-text-muted)]">
            By continuing, you acknowledge Harmonia&apos;s{" "}
            <a href="#" className="text-[var(--login-text-secondary)]">
              Privacy Policy
            </a>
            .
          </p>
        </div>
      </main>

      <Footer />
    </div>
  );
}
