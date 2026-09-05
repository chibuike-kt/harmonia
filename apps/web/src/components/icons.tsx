// Direct ports of the inline SVGs in docs/design/login-mockup-reference.html
// — same viewBoxes and path data, just componentized so Nav, Footer and
// the login card can share GitHub's icon instead of duplicating it.

export function ChevronDownIcon({ className }: { className?: string }) {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      className={className}
      aria-hidden="true"
    >
      <path d="M4 6l4 4 4-4" />
    </svg>
  );
}

export function GitHubIcon({ size = 18 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

export function GoogleIcon({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 18 18" aria-hidden="true">
      <path
        fill="#4285F4"
        d="M17.64 9.2c0-.637-.057-1.251-.164-1.84H9v3.481h4.844c-.209 1.125-.843 2.078-1.796 2.717v2.258h2.908c1.702-1.567 2.684-3.874 2.684-6.615z"
      />
      <path
        fill="#34A853"
        d="M9 18c2.43 0 4.467-.806 5.956-2.18l-2.908-2.259c-.806.54-1.837.86-3.048.86-2.344 0-4.328-1.584-5.036-3.711H.957v2.332C2.438 15.983 5.482 18 9 18z"
      />
      <path
        fill="#FBBC05"
        d="M3.964 10.71c-.18-.54-.282-1.117-.282-1.71s.102-1.17.282-1.71V4.958H.957C.347 6.173 0 7.548 0 9s.348 2.827.957 4.042l3.007-2.332z"
      />
      <path
        fill="#EA4335"
        d="M9 3.58c1.321 0 2.508.454 3.44 1.345l2.582-2.58C13.463.891 11.426 0 9 0 5.482 0 2.438 2.017.957 4.958L3.964 6.29C4.672 4.163 6.656 2.58 9 2.58z"
      />
    </svg>
  );
}

export function XIcon() {
  return (
    <svg
      width="17"
      height="17"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M18.9 2H22l-7.6 8.7L23.3 22h-7l-5.5-7.2L4.5 22H1.4l8.1-9.3L.7 2h7.2l5 6.6L18.9 2zm-1.2 18h1.7L7.4 4h-1.8l12.1 16z" />
    </svg>
  );
}

export function LinkedInIcon() {
  return (
    <svg
      width="17"
      height="17"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M20.45 20.45h-3.55v-5.57c0-1.33-.02-3.03-1.85-3.03-1.86 0-2.14 1.45-2.14 2.94v5.66H9.36V9h3.41v1.56h.05c.47-.9 1.63-1.85 3.36-1.85 3.6 0 4.27 2.37 4.27 5.45v6.29zM5.34 7.43a2.06 2.06 0 11.02-4.12 2.06 2.06 0 01-.02 4.12zM7.11 20.45H3.56V9h3.55v11.45z" />
    </svg>
  );
}

export function DiscordIcon() {
  return (
    <svg
      width="17"
      height="17"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M20.32 4.37a19.8 19.8 0 00-4.9-1.52.07.07 0 00-.08.04c-.21.38-.45.87-.61 1.26a18.3 18.3 0 00-5.48 0 12.6 12.6 0 00-.63-1.26.08.08 0 00-.08-.04 19.7 19.7 0 00-4.9 1.52.07.07 0 00-.03.03C1.2 8.9.4 13.3.79 17.64a.08.08 0 00.03.06 19.9 19.9 0 006 3.03.08.08 0 00.08-.03c.46-.63.87-1.3 1.22-2a.08.08 0 00-.04-.11 13 13 0 01-1.88-.9.08.08 0 01-.01-.13c.13-.09.25-.19.37-.29a.07.07 0 01.08-.01c3.93 1.8 8.18 1.8 12.06 0a.07.07 0 01.08.01c.12.1.24.2.37.29a.08.08 0 010 .13c-.6.35-1.23.65-1.89.9a.08.08 0 00-.04.12c.36.69.77 1.36 1.22 1.99a.08.08 0 00.09.03 19.8 19.8 0 006.01-3.03.08.08 0 00.03-.05c.46-5-.77-9.36-3.26-13.24a.06.06 0 00-.03-.03z" />
    </svg>
  );
}

export function GlobeIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="6.5" />
      <path d="M1.5 8h13M8 1.5c1.8 1.8 2.8 4.1 2.8 6.5s-1 4.7-2.8 6.5c-1.8-1.8-2.8-4.1-2.8-6.5S6.2 3.3 8 1.5z" />
    </svg>
  );
}

// A 3x3 dot grid ("app launcher" style) for the mobile nav trigger —
// deliberately not a hamburger, and echoes the dotted ThinkingOrb motif
// used for the brand mark right next to it.
export function MenuIcon() {
  const positions = [3, 9, 15];
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 18 18"
      fill="currentColor"
      aria-hidden="true"
    >
      {positions.flatMap((cy) =>
        positions.map((cx) => (
          <circle key={`${cx}-${cy}`} cx={cx} cy={cy} r="1.6" />
        )),
      )}
    </svg>
  );
}
