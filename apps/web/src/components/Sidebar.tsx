"use client";

import type { MouseEvent as ReactMouseEvent } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { apiFetch, ApiError } from "@/lib/api";
import {
  AgentsIcon,
  ActivityIcon,
  ChevronDownIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  GridIcon,
  HelpIcon,
  LogoutIcon,
  PlusIcon,
  SearchIcon,
  SecurityIcon,
  SettingsIcon,
  SortIcon,
  TeamIcon,
} from "./icons";

interface RoomSummary {
  id: string;
  name: string;
  last_activity_at: string;
  has_running_agent: boolean;
}

interface Me {
  id: string;
  username: string;
  display_name?: string;
  email?: string;
}

const MIN_WIDTH = 200;
const MAX_WIDTH = 420;
const DEFAULT_WIDTH = 272;

// Real destinations only — Activity has no backend or page yet (see
// docs/design/dashboard-build-brief.md's explicit scope note), kept
// inert exactly like the mockup's own "#" placeholder rather than
// inventing one.
const QUICK_NAV = [
  { label: "Dashboard", href: "/dashboard", Icon: GridIcon },
  { label: "New room", href: "/rooms/new", Icon: PlusIcon },
  { label: "Agents", href: "/connect-agents", Icon: AgentsIcon },
  { label: "Activity", href: "#", Icon: ActivityIcon },
] as const;

// None of these concepts (org/teams, security settings, a settings page,
// real search) exist in the backend yet — explicitly out of scope per
// the brief. Log out is handled separately below since it's a real action,
// not a link.
const PROFILE_PLACEHOLDER_ITEMS = [
  { label: "Search", Icon: SearchIcon },
  { label: "Team & roles", Icon: TeamIcon },
  { label: "Security", Icon: SecurityIcon },
] as const;
const PROFILE_PLACEHOLDER_ITEMS_2 = [
  { label: "Settings", Icon: SettingsIcon },
  { label: "Get help", Icon: HelpIcon },
] as const;

function formatRelativeTime(iso: string): string {
  const diffMinutes = Math.floor(
    (Date.now() - new Date(iso).getTime()) / 60000,
  );
  if (diffMinutes < 1) return "now";
  if (diffMinutes < 60) return `${diffMinutes}m`;
  const hours = Math.floor(diffMinutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}d`;
  return `${Math.floor(days / 7)}w`;
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

// Persistent app shell sidebar — port of docs/design/dashboard-mockup.html.
// Rendered once by the (shell) route group's layout, so it isn't
// remounted (and its collapse/width/rooms state isn't lost) as the user
// navigates between the dashboard and a room.
export function Sidebar() {
  const pathname = usePathname();
  const router = useRouter();

  const [collapsed, setCollapsed] = useState(false);
  const [width, setWidth] = useState(DEFAULT_WIDTH);
  const [resizing, setResizing] = useState(false);
  const [roomsCollapsed, setRoomsCollapsed] = useState(false);
  const [profileOpen, setProfileOpen] = useState(false);

  const [rooms, setRooms] = useState<RoomSummary[] | null>(null);
  const [me, setMe] = useState<Me | null>(null);

  const profileRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    // Same fetch-on-mount pattern as connect-agents' own load: a plain
    // client fetch, since auth here is a same-origin browser cookie the
    // Go backend reads directly — see that page's own comment for why a
    // Server Component fetch would need to forward it manually instead.
    void apiFetch<RoomSummary[]>("/v1/rooms")
      .then(setRooms)
      .catch(() => setRooms([]));
    // This doubles as the (shell) layout's auth guard: Sidebar is
    // rendered on every page inside the shell, so an expired/missing
    // session surfaces here first, the same client-fetch way every other
    // auth check in this app works — a Server Component layout would
    // need to forward the cookie manually to ask the same question. See
    // lib/useRequireAuth.ts's own comment for why a 401 logs out (clears
    // the httpOnly cookie for real) before redirecting, rather than just
    // pushing straight to /login — without that, a stale-but-present
    // cookie has middleware bounce straight back here on the next load.
    void apiFetch<Me>("/v1/users/me")
      .then(setMe)
      .catch(async (err: unknown) => {
        setMe(null);
        if (err instanceof ApiError && err.status === 401) {
          await apiFetch("/v1/auth/logout", { method: "POST" }).catch(() => {});
          router.push("/login");
        }
      });
  }, [router]);

  useEffect(() => {
    function onDocumentClick(event: MouseEvent) {
      if (
        profileRef.current &&
        !profileRef.current.contains(event.target as Node)
      ) {
        setProfileOpen(false);
      }
    }
    document.addEventListener("click", onDocumentClick);
    return () => document.removeEventListener("click", onDocumentClick);
  }, []);

  const handleResizeStart = useCallback(
    (event: ReactMouseEvent) => {
      event.preventDefault();
      if (collapsed) return;
      setResizing(true);
      const onMove = (moveEvent: globalThis.MouseEvent) => {
        setWidth(Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, moveEvent.clientX)));
      };
      const onUp = () => {
        setResizing(false);
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
      };
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
    },
    [collapsed],
  );

  const handleLogout = async () => {
    try {
      await apiFetch("/v1/auth/logout", { method: "POST" });
    } finally {
      router.push("/login");
    }
  };

  const displayName = me?.display_name || me?.username || "";

  return (
    <div
      className="group/shell relative h-screen shrink-0"
      style={{
        width: collapsed ? 0 : width,
        transition: resizing ? "none" : "width 0.18s ease",
        ["--peek-width" as string]: `${width}px`,
      }}
    >
      {!collapsed && (
        <div
          onMouseDown={handleResizeStart}
          className="absolute -right-[3px] top-0 z-30 h-full w-1.5 cursor-col-resize hover:bg-[var(--login-accent)]/35"
        />
      )}

      {collapsed && (
        <button
          type="button"
          aria-label="Show sidebar"
          onClick={() => setCollapsed(false)}
          className="absolute left-2.5 top-[18px] z-40 flex h-[30px] w-[30px] items-center justify-center rounded-md border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] text-[var(--login-text-secondary)] hover:bg-[#1C222B] hover:text-[var(--login-text)]"
        >
          <ChevronRightIcon />
        </button>
      )}

      {/* Collapsed: zero width, clipped, and absolutely positioned so it
          overlays main on hover instead of pushing it — main never
          reflows during a peek, only a click makes it permanent. */}
      <div
        className={
          collapsed
            ? "group/sidebar absolute left-0 top-0 z-[35] flex h-full w-0 flex-col overflow-hidden transition-[width,box-shadow] duration-150 ease-out group-hover/shell:w-[var(--peek-width)] group-hover/shell:border-r group-hover/shell:border-[var(--login-border)] group-hover/shell:shadow-[6px_0_32px_rgba(0,0,0,0.55)]"
            : "group/sidebar flex h-full w-full flex-col overflow-hidden border-r border-[var(--login-border)]"
        }
        style={{ background: "var(--login-sidebar-bg)" }}
      >
        <div className="flex items-center justify-between px-3.5 py-4 pb-3">
          <span className="whitespace-nowrap text-[17px] font-semibold tracking-[-0.01em] text-[var(--login-text)]">
            Harmonia
          </span>
          <button
            type="button"
            aria-label="Collapse sidebar"
            onClick={() => setCollapsed(true)}
            className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)]"
          >
            <ChevronLeftIcon />
          </button>
        </div>

        <nav className="flex flex-col gap-0.5 px-2.5">
          {QUICK_NAV.map(({ label, href, Icon }) => {
            const active = pathname === href;
            return (
              <Link
                key={label}
                href={href}
                className={`flex items-center gap-2.5 whitespace-nowrap rounded-lg px-2.5 py-2 text-sm ${
                  active
                    ? "bg-[var(--login-surface-2)] text-[var(--login-text)]"
                    : "text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
                }`}
              >
                <Icon />
                {label}
              </Link>
            );
          })}
        </nav>

        <div className="mx-3.5 my-3.5 h-px bg-[var(--login-border)]" />

        <div className="flex items-center justify-between py-2 pl-[18px] pr-3.5">
          <span className="font-[family-name:var(--login-font-mono)] text-xs text-[var(--login-text-muted)]">
            Rooms
          </span>
          <div className="flex items-center gap-0.5">
            <button
              type="button"
              aria-label="Collapse room history"
              onClick={() => setRoomsCollapsed((c) => !c)}
              className="flex rounded p-[3px] text-[var(--login-text-muted)] opacity-0 transition-opacity hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)] group-hover/sidebar:opacity-100"
            >
              <ChevronDownIcon
                className={roomsCollapsed ? "-rotate-90" : undefined}
              />
            </button>
            <button
              type="button"
              aria-label="Sort rooms"
              className="flex rounded p-[3px] text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)]"
            >
              <SortIcon />
            </button>
          </div>
        </div>

        {!roomsCollapsed && (
          <div className="flex min-h-0 flex-1 flex-col gap-px overflow-y-auto px-2.5">
            {rooms === null &&
              Array.from({ length: 3 }).map((_, i) => (
                <div
                  key={i}
                  className="mx-0.5 my-[3px] h-[34px] animate-pulse rounded-lg bg-[var(--login-surface-2)]/50"
                />
              ))}
            {rooms !== null && rooms.length === 0 && (
              <div className="flex flex-col items-start gap-2 px-2.5 py-3 text-sm text-[var(--login-text-muted)]">
                <p>No rooms yet.</p>
                <Link
                  href="/rooms/new"
                  className="text-[var(--login-text-secondary)] underline hover:text-[var(--login-text)]"
                >
                  Create your first room
                </Link>
              </div>
            )}
            {rooms?.map((room) => {
              const active = pathname === `/rooms/${room.id}`;
              return (
                <Link
                  key={room.id}
                  href={`/rooms/${room.id}`}
                  className={`flex items-center gap-2.5 whitespace-nowrap rounded-lg px-2.5 py-2 ${
                    active
                      ? "bg-[var(--login-surface-2)] text-[var(--login-text)]"
                      : "text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)]"
                  }`}
                >
                  {room.has_running_agent && (
                    <span className="h-[7px] w-[7px] shrink-0 rounded-full bg-[var(--login-accent)] shadow-[0_0_0_3px_rgba(76,211,194,0.15)]" />
                  )}
                  <span
                    className={`flex-1 overflow-hidden text-ellipsis text-sm ${active ? "text-[var(--login-text)]" : ""}`}
                  >
                    {room.name}
                  </span>
                  <span className="shrink-0 font-[family-name:var(--login-font-mono)] text-[11px] text-[var(--login-text-muted)]">
                    {formatRelativeTime(room.last_activity_at)}
                  </span>
                </Link>
              );
            })}
          </div>
        )}
        {roomsCollapsed && <div className="flex-1" />}

        <div
          ref={profileRef}
          className="relative mt-auto shrink-0 border-t border-[var(--login-border)] p-2.5"
        >
          {profileOpen && (
            <div className="absolute bottom-[calc(100%+6px)] left-2.5 right-2.5 flex flex-col gap-px rounded-[10px] border border-[var(--login-border-strong)] bg-[var(--login-surface)] p-1.5 shadow-[0_8px_24px_rgba(0,0,0,0.4)]">
              {PROFILE_PLACEHOLDER_ITEMS.map(({ label, Icon }) => (
                <a
                  key={label}
                  href="#"
                  className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13.5px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
                >
                  <Icon />
                  {label}
                </a>
              ))}
              <div className="mx-1 my-1 h-px bg-[var(--login-border)]" />
              {PROFILE_PLACEHOLDER_ITEMS_2.map(({ label, Icon }) => (
                <a
                  key={label}
                  href="#"
                  className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13.5px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
                >
                  <Icon />
                  {label}
                </a>
              ))}
              <div className="mx-1 my-1 h-px bg-[var(--login-border)]" />
              <button
                type="button"
                onClick={handleLogout}
                className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13.5px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
              >
                <LogoutIcon />
                Log out
              </button>
            </div>
          )}
          <button
            type="button"
            onClick={() => setProfileOpen((open) => !open)}
            className="flex w-full items-center gap-2.5 rounded-lg p-2 text-left hover:bg-[var(--login-surface-2)]"
          >
            <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] text-xs font-semibold text-[var(--login-accent)]">
              {displayName ? initials(displayName) : ""}
            </span>
            <span className="min-w-0 flex-1 overflow-hidden">
              <span className="block truncate text-[13.5px] font-medium text-[var(--login-text)]">
                {displayName || "…"}
              </span>
              <span className="block truncate text-[11.5px] text-[var(--login-text-muted)]">
                {me?.email ?? ""}
              </span>
            </span>
            <ChevronDownIcon className="rotate-180" />
          </button>
        </div>
      </div>
    </div>
  );
}
