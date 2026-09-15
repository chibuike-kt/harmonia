// Client-side-only record of previously-opened local IDE folders, and
// which Harmonia room (ADR-010: "an IDE session is still a room
// underneath") each one is paired with. Stored in localStorage, never
// server-side — a locally opened folder's path is inherently machine-
// specific, the exact same reasoning ADR-010 already applies to why
// migrations/0015_repo_connections was dropped rather than reused for
// this. Deliberately not shared across browsers/machines; that's a
// feature of this model, not a gap in it.

const STORAGE_KEY = "harmonia:ide:recents";
const MAX_RECENTS = 10;

export interface RecentFolder {
  path: string;
  roomId: string;
  name: string;
  lastOpened: string;
}

function readAll(): RecentFolder[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (r): r is RecentFolder =>
        typeof r === "object" &&
        r !== null &&
        typeof (r as RecentFolder).path === "string" &&
        typeof (r as RecentFolder).roomId === "string",
    );
  } catch {
    return [];
  }
}

function writeAll(recents: RecentFolder[]) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(recents));
  } catch {
    // Storage unavailable (private mode, quota) — Open Recent just stays
    // empty next time; nothing here needs to be durable.
  }
}

/** All recent folders, most recently opened first. */
export function listRecentFolders(): RecentFolder[] {
  return readAll().sort((a, b) => (a.lastOpened < b.lastOpened ? 1 : -1));
}

/** The room already paired with path, if this machine has opened it before. */
export function findRecentFolder(path: string): RecentFolder | undefined {
  return readAll().find((r) => r.path === path);
}

/** Records (or refreshes) path's pairing with roomId — call once a
 *  folder is opened, whether that room was just created or reused. */
export function rememberRecentFolder(path: string, roomId: string) {
  const name = path.replace(/[/\\]+$/, "").split(/[/\\]/).pop() || path;
  const existing = readAll().filter((r) => r.path !== path);
  const next = [
    { path, roomId, name, lastOpened: new Date().toISOString() },
    ...existing,
  ].slice(0, MAX_RECENTS);
  writeAll(next);
}
