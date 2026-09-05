import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

// Matches internal/user's SessionCookieName (internal/user/auth.go) —
// duplicated here rather than imported since this is a separate build
// (ADR-003: "one product, two independent build systems").
const SESSION_COOKIE = "harmonia_session";

const PROTECTED_PREFIXES = ["/dashboard", "/rooms", "/connect-agents"];

// Fast, cookie-presence-only redirect — this cannot tell an expired or
// revoked session from a valid one, only whether a cookie exists at all.
// Middleware runs on the Edge runtime with no access to the Go backend's
// database, so that's as far as it can go; useRequireAuth (see
// lib/useRequireAuth.ts) closes the gap with a real GET /v1/users/me
// check once the page itself loads.
export function middleware(request: NextRequest) {
  const hasSessionCookie = request.cookies.has(SESSION_COOKIE);
  const { pathname } = request.nextUrl;

  if (pathname === "/login") {
    if (hasSessionCookie) {
      return NextResponse.redirect(new URL("/dashboard", request.url));
    }
    return NextResponse.next();
  }

  const isProtected = PROTECTED_PREFIXES.some(
    (prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`),
  );
  if (isProtected && !hasSessionCookie) {
    return NextResponse.redirect(new URL("/login", request.url));
  }

  return NextResponse.next();
}

export const config = {
  matcher: [
    "/login",
    "/dashboard/:path*",
    "/rooms/:path*",
    "/connect-agents/:path*",
  ],
};
