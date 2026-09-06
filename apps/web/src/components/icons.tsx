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

// Dashboard/launcher icons — direct ports of the inline SVGs in
// docs/design/dashboard-mockup.html.

export function GridIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <rect x="2" y="2" width="5" height="5" rx="1" />
      <rect x="9" y="2" width="5" height="5" rx="1" />
      <rect x="2" y="9" width="5" height="5" rx="1" />
      <rect x="9" y="9" width="5" height="5" rx="1" />
    </svg>
  );
}

export function PlusIcon({
  size = 16,
  strokeWidth = 1.6,
}: {
  size?: number;
  strokeWidth?: number;
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      aria-hidden="true"
    >
      <path d="M8 2v12M2 8h12" />
    </svg>
  );
}

export function AgentsIcon({ size = 16 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <circle cx="8" cy="5" r="2.5" />
      <path d="M2.5 14c0-2.5 2.5-4 5.5-4s5.5 1.5 5.5 4" />
    </svg>
  );
}

export function ActivityIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M2 8h3l2-5 3 10 2-5h2" />
    </svg>
  );
}

export function ChevronLeftIcon() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      aria-hidden="true"
    >
      <path d="M10 3L5 8l5 5" />
    </svg>
  );
}

export function ChevronRightIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      aria-hidden="true"
    >
      <path d="M6 3l5 5-5 5" />
    </svg>
  );
}

export function SortIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M4 4h8M4 8h5M4 12h2" />
    </svg>
  );
}

export function SearchIcon() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <circle cx="7" cy="7" r="5" />
      <path d="M14 14l-3.5-3.5" />
    </svg>
  );
}

export function TeamIcon({ size = 15 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <circle cx="5.5" cy="6" r="2" />
      <circle cx="11" cy="6" r="1.8" />
      <path d="M1.5 14c0-2.2 1.8-3.5 4-3.5s4 1.3 4 3.5M10 10.7c1.8.1 3 1.2 3 3.3" />
    </svg>
  );
}

export function SecurityIcon() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M8 1.5l5 2v4c0 3.5-2.2 5.8-5 7-2.8-1.2-5-3.5-5-7v-4l5-2z" />
    </svg>
  );
}

export function SettingsIcon() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="2" />
      <path d="M8 1v2M8 13v2M2.5 8H1M15 8h-1.5M3.5 3.5l1 1M11.5 11.5l1 1M3.5 12.5l1-1M11.5 4.5l1-1" />
    </svg>
  );
}

export function HelpIcon() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="6.5" />
      <path d="M6 6a2 2 0 114 0c0 1.3-2 1.5-2 3M8 12h.01" />
    </svg>
  );
}

export function LogoutIcon() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M6 14H3.5A1.5 1.5 0 012 12.5v-9A1.5 1.5 0 013.5 2H6M10.5 11.5L14 8l-3.5-3.5M14 8H6" />
    </svg>
  );
}

export function HandoffIcon() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M2 8h4M10 8h4M6 4l-2 4 2 4M10 4l2 4-2 4" />
    </svg>
  );
}

export function WatchIcon() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M1.5 8s2.5-5 6.5-5 6.5 5 6.5 5-2.5 5-6.5 5-6.5-5-6.5-5z" />
      <circle cx="8" cy="8" r="2" />
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

// Three vertical dots — the row-hover "more actions" trigger, same glyph
// Claude's own history sidebar uses for this exact pattern.
export function MoreIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 14 14"
      fill="currentColor"
      aria-hidden="true"
    >
      <circle cx="7" cy="2.5" r="1.4" />
      <circle cx="7" cy="7" r="1.4" />
      <circle cx="7" cy="11.5" r="1.4" />
    </svg>
  );
}

export function PinIcon({ filled = false }: { filled?: boolean }) {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill={filled ? "currentColor" : "none"}
      stroke="currentColor"
      strokeWidth="1.3"
      aria-hidden="true"
    >
      <path d="M8 14.5s5-4.2 5-8a5 5 0 10-10 0c0 3.8 5 8 5 8z" />
    </svg>
  );
}

export function RenameIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M11 2l3 3-8 8-3.5 1 1-3.5 8-8z" />
    </svg>
  );
}

export function TrashIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M2.5 4h11M6 4V2.5h4V4M4.5 4l.5 9.5h6l.5-9.5" />
    </svg>
  );
}

// Room view icons — direct ports of the inline SVGs in
// docs/design/room-mockup.html.

export function CopyIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <rect x="5" y="5" width="9" height="9" rx="1.5" />
      <path d="M11 5V3.5A1.5 1.5 0 009.5 2h-6A1.5 1.5 0 002 3.5v6A1.5 1.5 0 003.5 11H5" />
    </svg>
  );
}

export function CodeBracketsIcon() {
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
      <path d="M5 3L1.5 8 5 13M11 3l3.5 5-3.5 5" />
    </svg>
  );
}

export function DownloadIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M8 2v8M4.5 7L8 10.5 11.5 7M3 13h10" />
    </svg>
  );
}

export function EnlargeIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M6 2H2v4M10 2h4v4M6 14H2v-4M10 14h4v-4" />
    </svg>
  );
}

export function CloseIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      aria-hidden="true"
    >
      <path d="M4 4l8 8M12 4l-8 8" />
    </svg>
  );
}

export function SendIcon() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      aria-hidden="true"
    >
      <path d="M2 8h11M9 4l4 4-4 4" />
    </svg>
  );
}

export function ReplyArrowIcon() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      aria-hidden="true"
    >
      <path d="M10 4L4 10M4 4v6h6" />
    </svg>
  );
}

export function WarnIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="6.5" />
      <path d="M8 5v4M8 11h.01" />
    </svg>
  );
}

export function FileIcon() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M9 2H4.5A1.5 1.5 0 003 3.5v9A1.5 1.5 0 004.5 14h7a1.5 1.5 0 001.5-1.5V6L9 2z" />
      <path d="M9 2v4h4" />
    </svg>
  );
}

// Room-header icons — direct ports of the inline SVGs in
// docs/design/room-mockup.html's room-header-right section.

export function InfoIcon({ className }: { className?: string }) {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      className={className}
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="6.5" />
      <path d="M8 7.5v4M8 5.2h.01" />
    </svg>
  );
}

export function CoinIcon() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="6" />
      <path d="M8 4.5v7M6 6.5h3a1.3 1.3 0 010 2.6H7a1.3 1.3 0 000 2.6h3" />
    </svg>
  );
}
