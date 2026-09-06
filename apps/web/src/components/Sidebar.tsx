"use client";

import type { MouseEvent as ReactMouseEvent } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { apiFetch, ApiError } from "@/lib/api";
import { useIsTruncated } from "@/lib/useIsTruncated";
import { Tooltip } from "./Tooltip";
import {
  AgentsIcon,
  ActivityIcon,
  ChevronDownIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  GridIcon,
  HelpIcon,
  LogoutIcon,
  MoreIcon,
  PinIcon,
  PlusIcon,
  RenameIcon,
  SearchIcon,
  SecurityIcon,
  SettingsIcon,
  SortIcon,
  TeamIcon,
  TrashIcon,
} from "./icons";

interface RoomSummary {
  id: string;
  name: string;
  last_activity_at: string;
  has_running_agent: boolean;
  pinned_at?: string;
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

interface RoomRowProps {
  room: RoomSummary;
  active: boolean;
  isRenaming: boolean;
  menuOpen: boolean;
  confirmingDelete: boolean;
  actionPending: boolean;
  actionError: string | null;
  onOpenMenu: () => void;
  onCloseMenu: () => void;
  onTogglePin: () => void;
  onStartRename: () => void;
  onCommitRename: (name: string) => void;
  onCancelRename: () => void;
  onRequestDelete: () => void;
  onCancelDelete: () => void;
  onConfirmDelete: () => void;
}

// One row of the rooms list. Presentational — Sidebar owns which row (if
// any) has its menu open, is renaming, or is mid-delete-confirmation,
// since only one of each can be active across the whole list at a time
// and that's much simpler to guarantee from one place than to coordinate
// across N independent row components.
function RoomRow({
  room,
  active,
  isRenaming,
  menuOpen,
  confirmingDelete,
  actionPending,
  actionError,
  onOpenMenu,
  onCloseMenu,
  onTogglePin,
  onStartRename,
  onCommitRename,
  onCancelRename,
  onRequestDelete,
  onCancelDelete,
  onConfirmDelete,
}: RoomRowProps) {
  const { ref: nameRef, truncated } = useIsTruncated<HTMLSpanElement>(
    room.name,
  );
  const skipBlurCommitRef = useRef(false);

  if (isRenaming) {
    return (
      <div className="flex items-center gap-2.5 rounded-lg bg-[var(--login-surface-2)] px-2.5 py-2">
        {room.has_running_agent && (
          <span className="h-[7px] w-[7px] shrink-0 rounded-full bg-[var(--login-accent)] shadow-[0_0_0_3px_rgba(76,211,194,0.15)]" />
        )}
        <input
          autoFocus
          defaultValue={room.name}
          onFocus={(e) => e.currentTarget.select()}
          onKeyDown={(e) => {
            if (e.key === "Enter") e.currentTarget.blur();
            if (e.key === "Escape") {
              skipBlurCommitRef.current = true;
              onCancelRename();
            }
          }}
          onBlur={(e) => {
            if (skipBlurCommitRef.current) {
              skipBlurCommitRef.current = false;
              return;
            }
            onCommitRename(e.currentTarget.value);
          }}
          className="min-w-0 flex-1 border-b border-[var(--login-accent)] bg-transparent text-sm text-[var(--login-text)] outline-none"
        />
      </div>
    );
  }

  return (
    // z-20 only while this row's own menu is open: `data-room-menu`'s
    // -translate-y-1/2 (a real `translate` value, not "none") gives it
    // its own stacking context, so the open dropdown's z-10 is trapped
    // inside that context and can't outrank *other rows* — those are
    // plain position:relative with z-index:auto, which stack by DOM
    // order, so a later row paints over an earlier row's open menu.
    // Promoting the whole row (not the dropdown) is what actually wins
    // that comparison, since it's the row-to-row ordering that's broken.
    <div className={`group/room relative ${menuOpen ? "z-20" : ""}`}>
      <Link
        href={`/rooms/${room.id}`}
        className={`flex items-center gap-2.5 whitespace-nowrap rounded-lg py-2 pl-2.5 pr-8 ${
          active
            ? "bg-[var(--login-surface-2)] text-[var(--login-text)]"
            : "text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)]"
        }`}
      >
        {room.pinned_at && <PinIcon filled />}
        {room.has_running_agent && (
          <span className="h-[7px] w-[7px] shrink-0 rounded-full bg-[var(--login-accent)] shadow-[0_0_0_3px_rgba(76,211,194,0.15)]" />
        )}
        <Tooltip
          label={room.name}
          disabled={!truncated}
          className="min-w-0 flex-1"
        >
          <span
            ref={nameRef}
            className={`block w-full overflow-hidden text-ellipsis text-sm ${active ? "text-[var(--login-text)]" : ""}`}
          >
            {room.name}
          </span>
        </Tooltip>
        <span
          className={`shrink-0 font-[family-name:var(--login-font-mono)] text-[11px] text-[var(--login-text-muted)] ${
            menuOpen ? "hidden" : "group-hover/room:hidden"
          }`}
        >
          {formatRelativeTime(room.last_activity_at)}
        </span>
      </Link>

      <div
        data-room-menu={room.id}
        className={`absolute right-1 top-1/2 -translate-y-1/2 ${
          menuOpen ? "flex" : "hidden group-hover/room:flex"
        }`}
      >
        <Tooltip label="More">
          <button
            type="button"
            aria-label="Room actions"
            onClick={() => (menuOpen ? onCloseMenu() : onOpenMenu())}
            className="flex h-6 w-6 items-center justify-center rounded text-[var(--login-text-muted)] hover:bg-[var(--login-border-strong)] hover:text-[var(--login-text)]"
          >
            <MoreIcon />
          </button>
        </Tooltip>

        {menuOpen && (
          <div className="absolute right-0 top-full z-10 mt-1 flex w-48 flex-col gap-px rounded-[10px] border border-[var(--login-border-strong)] bg-[var(--login-surface)] p-1.5 shadow-[0_8px_24px_rgba(0,0,0,0.4)]">
            {!confirmingDelete ? (
              <>
                <button
                  type="button"
                  disabled={actionPending}
                  onClick={onTogglePin}
                  className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13.5px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)] disabled:opacity-50"
                >
                  <PinIcon filled={!!room.pinned_at} />
                  {room.pinned_at ? "Unpin" : "Pin"}
                </button>
                <button
                  type="button"
                  disabled={actionPending}
                  onClick={onStartRename}
                  className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13.5px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)] disabled:opacity-50"
                >
                  <RenameIcon />
                  Rename
                </button>
                <div className="mx-1 my-1 h-px bg-[var(--login-border)]" />
                <button
                  type="button"
                  disabled={actionPending}
                  onClick={onRequestDelete}
                  className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13.5px] text-red-400 hover:bg-[var(--login-surface-2)] hover:text-red-300 disabled:opacity-50"
                >
                  <TrashIcon />
                  Delete
                </button>
              </>
            ) : (
              <div className="flex flex-col gap-2 px-2 py-1.5">
                <p className="text-[13px] text-[var(--login-text)]">
                  Delete this room? This can&apos;t be undone.
                </p>
                <div className="flex gap-1.5">
                  <button
                    type="button"
                    disabled={actionPending}
                    onClick={onCancelDelete}
                    className="flex-1 rounded-lg border border-[var(--login-border-strong)] px-2 py-1.5 text-[13px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)] disabled:opacity-50"
                  >
                    Cancel
                  </button>
                  <button
                    type="button"
                    disabled={actionPending}
                    onClick={onConfirmDelete}
                    className="flex-1 rounded-lg bg-red-500/90 px-2 py-1.5 text-[13px] font-medium text-white hover:bg-red-500 disabled:opacity-50"
                  >
                    {actionPending ? "Deleting…" : "Delete"}
                  </button>
                </div>
              </div>
            )}
            {actionError && (
              <p className="px-2.5 pt-1 text-[12px] text-red-400">
                {actionError}
              </p>
            )}
          </div>
        )}
      </div>
    </div>
  );
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

  const [openMenuFor, setOpenMenuFor] = useState<string | null>(null);
  const [confirmDeleteFor, setConfirmDeleteFor] = useState<string | null>(null);
  const [renamingRoomId, setRenamingRoomId] = useState<string | null>(null);
  const [actionPending, setActionPending] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const profileRef = useRef<HTMLDivElement>(null);

  const loadRooms = useCallback(() => {
    return apiFetch<RoomSummary[]>("/v1/rooms")
      .then(setRooms)
      .catch(() => setRooms([]));
  }, []);

  useEffect(() => {
    // Same fetch-on-mount pattern as connect-agents' own load: a plain
    // client fetch, since auth here is a same-origin browser cookie the
    // Go backend reads directly — see that page's own comment for why a
    // Server Component fetch would need to forward it manually instead.
    void loadRooms();
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
  }, [router, loadRooms]);

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

  useEffect(() => {
    // Same "ref must cover the trigger too, not just the panel" fix the
    // profile menu above needed — but with N rows instead of one fixed
    // element, a data-attribute query is simpler than juggling N refs.
    function onDocumentClick(event: MouseEvent) {
      const target = event.target as HTMLElement;
      if (!target.closest("[data-room-menu]")) {
        setOpenMenuFor(null);
        setConfirmDeleteFor(null);
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

  const handleOpenMenu = (roomId: string) => {
    setActionError(null);
    setConfirmDeleteFor(null);
    setOpenMenuFor(roomId);
  };
  const handleCloseMenu = () => {
    setOpenMenuFor(null);
    setConfirmDeleteFor(null);
    setActionError(null);
  };

  const handleTogglePin = async (room: RoomSummary) => {
    setActionPending(true);
    setActionError(null);
    try {
      await apiFetch(`/v1/rooms/${room.id}`, {
        method: "PATCH",
        body: { pinned: !room.pinned_at },
      });
      await loadRooms();
      setOpenMenuFor(null);
    } catch (err) {
      setActionError(
        err instanceof ApiError ? err.message : "Failed to update room.",
      );
    } finally {
      setActionPending(false);
    }
  };

  const handleStartRename = (roomId: string) => {
    setOpenMenuFor(null);
    setRenamingRoomId(roomId);
  };

  const handleCommitRename = async (room: RoomSummary, rawValue: string) => {
    const name = rawValue.trim();
    setRenamingRoomId(null);
    if (!name || name === room.name) return;
    try {
      await apiFetch(`/v1/rooms/${room.id}`, {
        method: "PATCH",
        body: { name },
      });
      await loadRooms();
    } catch (err) {
      setActionError(
        err instanceof ApiError ? err.message : "Failed to rename room.",
      );
    }
  };

  const handleConfirmDelete = async (roomId: string) => {
    setActionPending(true);
    setActionError(null);
    try {
      await apiFetch(`/v1/rooms/${roomId}`, { method: "DELETE" });
      await loadRooms();
      setOpenMenuFor(null);
      setConfirmDeleteFor(null);
      if (pathname === `/rooms/${roomId}`) {
        router.push("/dashboard");
      }
    } catch (err) {
      setActionError(
        err instanceof ApiError ? err.message : "Failed to delete room.",
      );
    } finally {
      setActionPending(false);
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
      {/* Positioning lives on this outer div, not passed into Tooltip's
          own className: Tooltip's wrapper hardcodes position:relative
          (it's the anchor for the floating label), and concatenating an
          "absolute" utility into that same class list is a real
          position-property conflict — whichever wins depends on
          Tailwind's generated stylesheet order, not JSX order, and here
          it silently broke the whole sidebar's layout (relative won,
          the h-full resize handle occupied real flow space instead of
          being taken out of it, pushing everything below it down by a
          full viewport height). Keeping Tooltip itself simple and doing
          absolute positioning one level up avoids the conflict entirely. */}
      {!collapsed && (
        <div className="absolute -right-[3px] top-0 z-30 h-full">
          <Tooltip label="Resize sidebar" className="h-full">
            <div
              onMouseDown={handleResizeStart}
              className="h-full w-1.5 cursor-col-resize hover:bg-[var(--login-accent)]/35"
            />
          </Tooltip>
        </div>
      )}

      {collapsed && (
        <div className="absolute left-2.5 top-[18px] z-40">
          <Tooltip label="Show sidebar">
            <button
              type="button"
              aria-label="Show sidebar"
              onClick={() => setCollapsed(false)}
              className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] text-[var(--login-text-secondary)] hover:bg-[#1C222B] hover:text-[var(--login-text)]"
            >
              <ChevronRightIcon />
            </button>
          </Tooltip>
        </div>
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
          <Tooltip label="Collapse sidebar">
            <button
              type="button"
              aria-label="Collapse sidebar"
              onClick={() => setCollapsed(true)}
              className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)]"
            >
              <ChevronLeftIcon />
            </button>
          </Tooltip>
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
            <Tooltip label={roomsCollapsed ? "Expand rooms" : "Collapse rooms"}>
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
            </Tooltip>
            <Tooltip label="Sort rooms">
              <button
                type="button"
                aria-label="Sort rooms"
                className="flex rounded p-[3px] text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)]"
              >
                <SortIcon />
              </button>
            </Tooltip>
          </div>
        </div>

        {!roomsCollapsed && (
          // overflow-x-hidden is load-bearing, not decorative: a room
          // name's Tooltip renders its floating label at full,
          // untruncated width even while invisible (opacity-0, not
          // hovered), positioned via absolute + centered transform on a
          // ~180px anchor. With only overflow-y-auto set, the CSS
          // interop rule that promotes a lone axis's overflow to "auto"
          // makes this container the nearest scroll boundary, and that
          // always-present hidden label's width becomes real horizontal
          // scroll content — the sidebar scrolls sideways for a long
          // name even though the name span itself truncates correctly.
          <div className="flex min-h-0 flex-1 flex-col gap-px overflow-x-hidden overflow-y-auto px-2.5">
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
            {rooms?.map((room) => (
              <RoomRow
                key={room.id}
                room={room}
                active={pathname === `/rooms/${room.id}`}
                isRenaming={renamingRoomId === room.id}
                menuOpen={openMenuFor === room.id}
                confirmingDelete={confirmDeleteFor === room.id}
                actionPending={actionPending}
                actionError={openMenuFor === room.id ? actionError : null}
                onOpenMenu={() => handleOpenMenu(room.id)}
                onCloseMenu={handleCloseMenu}
                onTogglePin={() => void handleTogglePin(room)}
                onStartRename={() => handleStartRename(room.id)}
                onCommitRename={(name) => void handleCommitRename(room, name)}
                onCancelRename={() => setRenamingRoomId(null)}
                onRequestDelete={() => setConfirmDeleteFor(room.id)}
                onCancelDelete={() => setConfirmDeleteFor(null)}
                onConfirmDelete={() => void handleConfirmDelete(room.id)}
              />
            ))}
          </div>
        )}
        {roomsCollapsed && <div className="flex-1" />}

        <div
          ref={profileRef}
          className="relative mt-auto shrink-0 border-t border-[var(--login-border)] p-2.5"
        >
          {profileOpen && (
            <div className="absolute bottom-[calc(100%+6px)] left-2.5 right-2.5 flex flex-col gap-px rounded-[10px] border border-[var(--login-border-strong)] bg-[var(--login-surface)] p-1.5 shadow-[0_8px_24px_rgba(0,0,0,0.4)]">
              {me?.email && (
                <div className="truncate border-b border-[var(--login-border)] px-2.5 pb-2 pt-1 text-[12px] text-[var(--login-text-muted)]">
                  {me.email}
                </div>
              )}
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
