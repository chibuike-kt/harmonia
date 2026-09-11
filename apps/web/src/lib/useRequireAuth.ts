"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { apiFetch, ApiError } from "./api";

/**
 * Validates the session for real via GET /v1/users/me and redirects to
 * /login on a 401. proxy.ts's cookie-presence check runs first and
 * catches a missing cookie, but it can't tell an expired or revoked
 * session from a valid one — only an actual request to the backend can,
 * which is exactly the gap this closes.
 *
 * On a 401 this also calls POST /v1/auth/logout before redirecting —
 * not just router.push("/login") on its own. The cookie is httpOnly, so
 * this page can't clear it with document.cookie; if it's left in place,
 * the very next navigation to /login has proxy.ts see "cookie
 * present" and bounce straight back to a protected route, which 401s
 * again and pushes back to /login — an infinite redirect loop. Logout
 * unconditionally clears the cookie via a real Set-Cookie response even
 * when the session is already revoked or gone (RevokeSessionByTokenHash
 * treats "0 rows matched" as success, not an error — see
 * internal/user/auth.go), so this is safe to call speculatively here.
 *
 * Call this from every page that assumes a session and isn't already
 * covered by the (shell) layout's own Sidebar (which does the same
 * check as a side effect of loading the profile row's real data).
 */
export function useRequireAuth() {
  const router = useRouter();
  useEffect(() => {
    apiFetch("/v1/users/me").catch(async (err: unknown) => {
      if (err instanceof ApiError && err.status === 401) {
        await apiFetch("/v1/auth/logout", { method: "POST" }).catch(() => {});
        router.push("/login");
      }
    });
  }, [router]);
}
