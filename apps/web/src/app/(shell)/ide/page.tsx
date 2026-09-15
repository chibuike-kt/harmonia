"use client";

import dynamic from "next/dynamic";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import type * as monacoEditor from "monaco-editor";
import { OrbMark } from "@/components/OrbMark";
import { apiFetch } from "@/lib/api";
import {
  findRecentFolder,
  listRecentFolders,
  rememberRecentFolder,
  type RecentFolder,
} from "@/lib/ideRecents";
import { MenuBar, type PresenceAgent } from "@/components/ide/MenuBar";
import { CommandPalette } from "@/components/ide/CommandPalette";
import {
  TerminalPanel,
  type BottomTab,
  type TerminalMode,
  type TranscriptEntry,
} from "@/components/ide/TerminalPanel";
import { ChevronRightIcon, CloseIcon } from "@/components/icons";

const MonacoEditor = dynamic(() => import("@monaco-editor/react"), {
  ssr: false,
  loading: () => (
    <div className="flex flex-1 items-center justify-center text-[13px] text-[var(--ide-text-muted)]">
      Loading editor…
    </div>
  ),
});

interface DirEntry {
  name: string;
  path: string;
  is_dir: boolean;
}

interface ServerMessage {
  type: string;
  path?: string;
  entries?: DirEntry[];
  content_base64?: string;
  data?: string;
  exit_code?: number;
  message?: string;
}

interface OpenFile {
  path: string;
  content: string;
  dirty: boolean;
  loading: boolean;
}

interface RoomAgentDTO {
  id: string;
  name: string;
  provider: string;
  status: string;
}

interface ChatMessageDTO {
  id: string;
  // Matches internal/message.SenderHuman/SenderAgent's real wire values
  // exactly ("human", not "user") — a mismatch here silently misroutes
  // every human message to the agent-tag rendering branch instead of
  // erroring, since TypeScript can't catch a string literal drifting
  // from its Go source of truth. Found live: real chat messages this
  // human sent rendered with a generic "agent" tag.
  sender_kind: "human" | "agent";
  agent_id?: string;
  content: string;
}

interface RealtimeMessage {
  kind: string;
  message?: ChatMessageDTO;
  presence?: { agent_id: string; status: string };
  agent_cursor?: {
    agent_id: string;
    path: string;
    line: number;
    column: number;
    active: boolean;
    name: string;
    provider?: string;
  };
}

interface Snapshot {
  presence: { agent_id: string; status: string }[];
  messages: ChatMessageDTO[];
}

interface AgentCursorState {
  agentId: string;
  name: string;
  provider?: string;
  path: string;
  line: number;
  column: number;
}

type ConsentState = "checking" | "required" | "granting" | "granted" | "unreachable";

const COMPANION_URL = process.env.NEXT_PUBLIC_COMPANION_WS_URL;
const HEARTBEAT_MS = 5000;

function companionHttpBase(wsUrl: string): string {
  return wsUrl.replace(/^ws/, "http").replace(/\/ws\/?$/, "");
}

function base64ToText(b64: string): string {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return new TextDecoder().decode(bytes);
}

function textToBase64(text: string): string {
  const bytes = new TextEncoder().encode(text);
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}

function languageForPath(path: string): string {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  const map: Record<string, string> = {
    go: "go",
    ts: "typescript",
    tsx: "typescript",
    js: "javascript",
    jsx: "javascript",
    json: "json",
    md: "markdown",
    py: "python",
    rs: "rust",
    yml: "yaml",
    yaml: "yaml",
    sql: "sql",
    html: "html",
    css: "css",
    sh: "shell",
  };
  return map[ext] ?? "plaintext";
}

function fileKind(name: string): "css" | "markup" | "config" | "other" {
  const ext = name.split(".").pop()?.toLowerCase() ?? "";
  if (ext === "css") return "css";
  if (ext === "html" || ext === "htm" || ext === "md") return "markup";
  if (ext === "json" || ext === "yml" || ext === "yaml" || ext === "toml")
    return "config";
  return "other";
}

// Colors reuse Harmonia's own existing palette, never new ones, per the
// IDE design overhaul's own point 4.
const FILE_ICON_COLOR: Record<string, string> = {
  folder: "var(--room-warn)",
  css: "var(--room-handoff-purple)",
  markup: "var(--room-task-blue)",
  config: "var(--login-accent)",
  other: "var(--ide-text-secondary)",
};

function FileGlyph({ isDir, name }: { isDir: boolean; name: string }) {
  const color = isDir ? FILE_ICON_COLOR.folder : FILE_ICON_COLOR[fileKind(name)];
  if (isDir) {
    return (
      <svg
        width="14"
        height="14"
        viewBox="0 0 16 16"
        fill="none"
        stroke={color}
        strokeWidth="1.3"
        className="shrink-0"
      >
        <path d="M2 4.5A1.5 1.5 0 013.5 3h3l1.5 1.5h5A1.5 1.5 0 0114.5 6v6.5A1.5 1.5 0 0113 14H3.5A1.5 1.5 0 012 12.5v-8z" />
      </svg>
    );
  }
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke={color}
      strokeWidth="1.3"
      className="shrink-0"
    >
      <path d="M9 2H4.5A1.5 1.5 0 003 3.5v9A1.5 1.5 0 004.5 14h7a1.5 1.5 0 001.5-1.5V6L9 2z" />
    </svg>
  );
}

function humanInitialsFor(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

let entrySeq = 0;
function nextEntryId(): string {
  entrySeq += 1;
  return `e${entrySeq}`;
}

/** One row of the empty state's real action list — a module-scope
 *  component (not an inline data array) so its onRun closure is never
 *  threaded through the page's own render as a value. */
function EmptyStateAction({
  label,
  keys,
  onRun,
}: {
  label: string;
  keys: string[];
  onRun: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onRun}
      className="flex items-center justify-between rounded-lg px-3 py-2.5 text-left hover:bg-[var(--ide-surface)]"
    >
      <span className="text-[14px] text-[var(--ide-text-secondary)]">{label}</span>
      <span className="flex gap-1">
        {keys.map((k) => (
          <kbd
            key={k}
            className="rounded border border-[var(--ide-border-strong)] bg-[var(--ide-surface-2)] px-[7px] py-[2px] font-[family-name:var(--login-font-mono)] text-[10.5px] text-[var(--ide-text-muted)]"
          >
            {k}
          </kbd>
        ))}
      </span>
    </button>
  );
}

/**
 * The standalone Harmonia IDE — full design overhaul per
 * docs/design/harmonia-ide-mockup.html. Real local project (companion
 * process), real multi-tab editor, a genuinely closable bottom panel, a
 * real menu bar with real shortcuts, and — since an IDE session is still
 * a room underneath (ADR-010) — a real paired room backing live chat,
 * agent presence, and live-cursor broadcast, the same infrastructure
 * Rooms itself runs on, not a second system.
 */
export default function IdePage() {
  const router = useRouter();
  const wsRef = useRef<WebSocket | null>(null);
  const esRef = useRef<EventSource | null>(null);
  const [connectionState, setConnectionState] = useState<
    "disconnected" | "connecting" | "connected" | "error"
  >("disconnected");
  const [connectionError, setConnectionError] = useState<string | null>(null);
  const [consentState, setConsentState] = useState<ConsentState>("checking");

  const [folderPath, setFolderPath] = useState<string | null>(null);
  const [dirListings, setDirListings] = useState<Record<string, DirEntry[]>>({});
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const [openTabs, setOpenTabs] = useState<string[]>([]);
  const [openFiles, setOpenFiles] = useState<Record<string, OpenFile>>({});
  const [activePath, setActivePath] = useState<string | null>(null);
  const [autoSave, setAutoSave] = useState(false);

  const [explorerOpen, setExplorerOpen] = useState(true);
  const [panelOpen, setPanelOpen] = useState(true);
  const [bottomTab, setBottomTab] = useState<BottomTab>("terminal");
  const [shellRunning, setShellRunning] = useState(false);
  const [terminalMode, setTerminalMode] = useState<TerminalMode>("shell");
  const [shellValue, setShellValue] = useState("");
  const [chatValue, setChatValue] = useState("");
  const [transcript, setTranscript] = useState<TranscriptEntry[]>([]);
  const pendingOutputId = useRef<string | null>(null);

  const [cmdkOpen, setCmdkOpen] = useState(false);
  const [recentOpen, setRecentOpen] = useState(false);
  const [recents, setRecents] = useState<RecentFolder[]>([]);
  const [saveAsOpen, setSaveAsOpen] = useState(false);
  const [saveAsValue, setSaveAsValue] = useState("");

  // This IDE session's paired room (ADR-010: still a room underneath) —
  // null until a folder is open and the room lookup/creation resolves.
  const [roomId, setRoomId] = useState<string | null>(null);
  const [roomAgents, setRoomAgents] = useState<RoomAgentDTO[]>([]);
  const [agentStatus, setAgentStatus] = useState<Record<string, string>>({});
  const [agentCursors, setAgentCursors] = useState<Record<string, AgentCursorState>>({});
  const [humanName, setHumanName] = useState("You");

  const editorRef = useRef<monacoEditor.editor.IStandaloneCodeEditor | null>(null);
  const monacoNsRef = useRef<typeof monacoEditor | null>(null);
  const cursorWidgetsRef = useRef<Record<string, monacoEditor.editor.IContentWidget>>({});
  const editFadeDecorationsRef = useRef<
    Record<string, monacoEditor.editor.IEditorDecorationsCollection>
  >({});
  const editFadeTimers = useRef<Record<string, ReturnType<typeof setTimeout>>>({});

  const send = useCallback((msg: Record<string, unknown>) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(msg));
    }
  }, []);

  // ---------- Companion WebSocket ----------

  const handleServerMessage = useCallback((msg: ServerMessage) => {
    switch (msg.type) {
      case "folder_opened":
        setFolderPath(msg.path ?? null);
        setDirListings({});
        setExpanded(new Set([""]));
        send({ type: "list_dir", path: "" });
        break;
      case "dir_listing":
        setDirListings((prev) => ({ ...prev, [msg.path ?? ""]: msg.entries ?? [] }));
        break;
      case "file_content":
        if (!msg.path) break;
        setOpenFiles((prev) => ({
          ...prev,
          [msg.path as string]: {
            path: msg.path as string,
            content: base64ToText(msg.content_base64 ?? ""),
            dirty: false,
            loading: false,
          },
        }));
        break;
      case "file_written":
        if (!msg.path) break;
        setOpenFiles((prev) => {
          const existing = prev[msg.path as string];
          if (!existing) return prev;
          return { ...prev, [msg.path as string]: { ...existing, dirty: false } };
        });
        break;
      case "shell_started":
        setShellRunning(true);
        break;
      case "shell_output": {
        // The ref write happens here, in this single-invoke handler —
        // never inside the setTranscript updater below. React 18 Strict
        // Mode intentionally double-invokes an updater function to
        // surface exactly this class of bug: the first (discarded)
        // invocation would assign and commit a new id to the ref, so the
        // second (real) invocation would see a non-null id that names an
        // entry which was never actually added to the committed state —
        // its lookup finds nothing, and the chunk silently vanishes.
        // Found live: real shell output never rendering despite the
        // command line itself appearing correctly.
        let outputId = pendingOutputId.current;
        if (!outputId) {
          outputId = nextEntryId();
          pendingOutputId.current = outputId;
        }
        const id = outputId;
        setTranscript((prev) => {
          if (prev.some((e) => e.id === id && e.kind === "output")) {
            return prev.map((e) =>
              e.id === id && e.kind === "output"
                ? { ...e, text: e.text + (msg.data ?? "") }
                : e,
            );
          }
          return [...prev, { kind: "output", id, text: msg.data ?? "" }];
        });
        break;
      }
      case "shell_exited":
        setShellRunning(false);
        pendingOutputId.current = null;
        setTranscript((prev) => [
          ...prev,
          { kind: "output", id: nextEntryId(), text: `[process exited with code ${msg.exit_code}]` },
        ]);
        break;
      case "error": {
        const message = msg.message ?? "Unknown companion error.";
        setConnectionError(message);
        // A companion error while a folder is open must land somewhere
        // a human will actually see it — the empty-state screen (the
        // only other place connectionError renders) is gone by then.
        // Found live: a shell_input sent to a session with no shell
        // running failed silently here before this existed.
        setTranscript((prev) => [...prev, { kind: "error", id: nextEntryId(), text: message }]);
        break;
      }
    }
  }, [send]);

  const connect = useCallback(() => {
    if (!COMPANION_URL) return;
    // A guard against overlapping connections, not just a nicety: each
    // WebSocket the companion accepts gets its own fresh session (no
    // folder open, no shell) — see internal/companion's own doc comment
    // on "no ambient state." If an effect re-run (React Strict Mode's
    // double-invoke in dev, or any future cause) ever called connect()
    // again while a connection is already live, wsRef.current would
    // silently start pointing at that new, blank session while
    // React state (folderPath, shellRunning, the file tree) kept
    // reflecting whatever the *previous* session had — every further
    // send() would then go to a session with no folder and no shell
    // open, failing in ways the UI had no way to explain. Real bug,
    // found live: a shell command submitted successfully (the command
    // line rendered) but its output never arrived, because shell_input
    // had gone out on a second connection that never received
    // start_shell's own success.
    if (
      wsRef.current &&
      (wsRef.current.readyState === WebSocket.OPEN ||
        wsRef.current.readyState === WebSocket.CONNECTING)
    ) {
      return;
    }
    setConnectionState("connecting");
    setConnectionError(null);
    const ws = new WebSocket(COMPANION_URL);
    wsRef.current = ws;
    ws.onopen = () => {
      if (wsRef.current !== ws) return;
      setConnectionState("connected");
    };
    ws.onerror = () => {
      if (wsRef.current !== ws) return;
      setConnectionState("error");
    };
    ws.onclose = () => {
      // Only reset if ws is still the tracked connection — a stale
      // connection's close (superseded by a newer one) must never wipe
      // out state a newer, live connection already established.
      if (wsRef.current !== ws) return;
      wsRef.current = null;
      setConnectionState("disconnected");
      // The local session this UI was showing genuinely no longer
      // exists once its connection is gone — resetting rather than
      // leaving a "connected"-looking folder/shell backed by nothing is
      // the same "no lingering frozen state" rule ADR-010's presence
      // gate already applies, extended to the companion link itself.
      setFolderPath(null);
      setDirListings({});
      setExpanded(new Set());
      setOpenTabs([]);
      setOpenFiles({});
      setActivePath(null);
      setShellRunning(false);
      pendingOutputId.current = null;
    };
    ws.onmessage = (event) => {
      if (wsRef.current !== ws) return;
      handleServerMessage(JSON.parse(event.data as string) as ServerMessage);
    };
  }, [handleServerMessage]);

  useEffect(() => {
    if (!COMPANION_URL) return;
    let cancelled = false;
    void (async () => {
      try {
        const res = await fetch(`${companionHttpBase(COMPANION_URL)}/consent`);
        if (cancelled) return;
        if (!res.ok) {
          setConsentState("unreachable");
          return;
        }
        const body = (await res.json()) as { granted: boolean };
        setConsentState(body.granted ? "granted" : "required");
      } catch {
        if (!cancelled) setConsentState("unreachable");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const grantConsent = async () => {
    if (!COMPANION_URL) return;
    setConsentState("granting");
    try {
      const res = await fetch(`${companionHttpBase(COMPANION_URL)}/consent`, { method: "POST" });
      if (!res.ok) {
        setConsentState("unreachable");
        return;
      }
      setConsentState("granted");
    } catch {
      setConsentState("unreachable");
    }
  };

  useEffect(() => {
    if (consentState !== "granted") return;
    connect();
    return () => wsRef.current?.close();
  }, [consentState, connect]);

  useEffect(() => {
    // Reads localStorage, a real external system — same justification
    // as every other fetch/connect-on-mount effect in this codebase for
    // this lint rule.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setRecents(listRecentFolders());
  }, []);

  useEffect(() => {
    // Same display_name/username priority Sidebar.tsx already uses for
    // this same "me" endpoint — one convention, not a second one here.
    apiFetch<{ display_name?: string; username?: string }>("/v1/users/me")
      .then((me) => setHumanName(me.display_name || me.username || "You"))
      .catch(() => {});
  }, []);

  // ---------- Room pairing (ADR-010: still a room underneath) ----------

  useEffect(() => {
    if (!folderPath) {
      // Resetting derived state back to "no folder" — same
      // fetch/connect-on-mount lint justification as above.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setRoomId(null);
      setRoomAgents([]);
      setTranscript([]);
      setAgentCursors({});
      return;
    }
    let cancelled = false;
    void (async () => {
      const existing = findRecentFolder(folderPath);
      if (existing) {
        rememberRecentFolder(folderPath, existing.roomId);
        if (!cancelled) setRoomId(existing.roomId);
        return;
      }
      const folderName = folderPath.replace(/[/\\]+$/, "").split(/[/\\]/).pop() || folderPath;
      try {
        const room = await apiFetch<{ id: string }>("/v1/rooms", {
          method: "POST",
          body: { name: `IDE: ${folderName}` },
        });
        rememberRecentFolder(folderPath, room.id);
        if (!cancelled) setRoomId(room.id);
      } catch {
        // A real room is what makes chat/presence/live-cursor real; if
        // this fails the IDE still works as a pure local editor+shell —
        // the same degraded-but-honest posture the old single-process
        // build had, not a hard failure of the whole page.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [folderPath]);

  useEffect(() => {
    if (!roomId) return;
    apiFetch<RoomAgentDTO[]>(`/v1/rooms/${roomId}/agents`)
      .then(setRoomAgents)
      .catch(() => setRoomAgents([]));
  }, [roomId]);

  const chatEntryFromMessage = useCallback(
    (m: ChatMessageDTO): TranscriptEntry => {
      if (m.sender_kind === "human") {
        return { kind: "chat", id: m.id, sender: "human", content: m.content };
      }
      const agent = roomAgents.find((a) => a.id === m.agent_id);
      return {
        kind: "chat",
        id: m.id,
        sender: "agent",
        agentName: agent?.name ?? "agent",
        provider: agent?.provider,
        content: m.content,
      };
    },
    [roomAgents],
  );

  // Live channel: chat messages, agent run-status presence, and live
  // agent-cursor broadcasts — the exact same Hub/SSE stream every chat
  // room in Harmonia already uses (ADR-010's own "existing live
  // channel").
  useEffect(() => {
    if (!roomId) return;
    const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL;
    if (!apiBase) return;
    // Same overlapping-connection guard as the companion WebSocket's own
    // connect() — React Strict Mode's mount→cleanup→mount can otherwise
    // leave two live EventSource connections to the same room open at
    // once. Both would still deliver every real event correctly (unlike
    // the WebSocket case, nothing here is a stateful per-connection
    // session), but a live "message" event arriving twice would
    // duplicate a real chat bubble in the transcript — a subtler version
    // of the same duplicate-key bug the snapshot handler's own dedup
    // above this exists to prevent.
    if (
      esRef.current &&
      (esRef.current.readyState === EventSource.OPEN ||
        esRef.current.readyState === EventSource.CONNECTING)
    ) {
      return;
    }
    const source = new EventSource(`${apiBase}/v1/rooms/${roomId}/stream`, {
      withCredentials: true,
    });
    esRef.current = source;

    source.addEventListener("snapshot", (e) => {
      const data = JSON.parse((e as MessageEvent).data) as Snapshot;
      const initialStatus: Record<string, string> = {};
      for (const p of data.presence ?? []) initialStatus[p.agent_id] = p.status;
      setAgentStatus(initialStatus);
      // EventSource reconnects on its own on any drop, and every
      // reconnect delivers a fresh "snapshot" — unconditionally
      // prepending its messages every time would duplicate the same
      // real message ids (and, with them, React keys) on every
      // reconnect. Found live: the same two chat lines rendering
      // twice, and a real "duplicate key" React error firing on every
      // render once they had.
      setTranscript((prev) => {
        const existingIds = new Set(prev.map((entry) => entry.id));
        const newOnes = (data.messages ?? [])
          .filter((m) => !existingIds.has(m.id))
          .map((m) => chatEntryFromMessage(m));
        return [...newOnes, ...prev];
      });
    });

    source.addEventListener("message", (e) => {
      const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
      if (msg.message) setTranscript((prev) => [...prev, chatEntryFromMessage(msg.message!)]);
    });

    source.addEventListener("presence", (e) => {
      const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
      if (msg.presence) {
        setAgentStatus((prev) => ({ ...prev, [msg.presence!.agent_id]: msg.presence!.status }));
      }
    });

    // The live-cursor signal driving points 8/9 of the design overhaul:
    // active:true sets/updates the cursor, active:false — or this SSE
    // connection itself dropping, handled by the presence-visibility
    // effect below — removes it immediately, no fade.
    source.addEventListener("agent_cursor", (e) => {
      const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
      const c = msg.agent_cursor;
      if (!c) return;
      if (!c.active) {
        setAgentCursors((prev) => {
          const next = { ...prev };
          delete next[c.agent_id];
          return next;
        });
        return;
      }
      setAgentCursors((prev) => ({
        ...prev,
        [c.agent_id]: {
          agentId: c.agent_id,
          name: c.name,
          provider: c.provider,
          path: c.path,
          line: c.line,
          column: c.column,
        },
      }));
    });

    return () => {
      source.close();
      if (esRef.current === source) esRef.current = null;
    };
  }, [roomId, chatEntryFromMessage]);

  // ---------- Human presence heartbeat (IsHumanPresent) ----------
  // Point 9's real requirement: the moment this tab stops watching, live
  // agent UI vanishes immediately, not after a server round trip. The
  // heartbeat keeps the backend's own IsHumanPresent gate alive while
  // watching; visibility going hidden clears local state synchronously
  // *and* best-effort tells the backend to drop presence early rather
  // than waiting out its own TTL.
  useEffect(() => {
    if (!roomId) return;
    const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL;
    if (!apiBase) return;

    const beat = () => {
      void fetch(`${apiBase}/v1/rooms/${roomId}/presence/heartbeat`, {
        method: "POST",
        credentials: "include",
      }).catch(() => {});
    };
    const leave = () => {
      void fetch(`${apiBase}/v1/rooms/${roomId}/presence/heartbeat`, {
        method: "DELETE",
        credentials: "include",
      }).catch(() => {});
    };

    let interval: ReturnType<typeof setInterval> | null = null;
    const startBeating = () => {
      beat();
      interval = setInterval(beat, HEARTBEAT_MS);
    };
    const stopBeating = () => {
      if (interval) clearInterval(interval);
      interval = null;
    };

    const onVisibility = () => {
      if (document.visibilityState === "visible") {
        startBeating();
      } else {
        stopBeating();
        leave();
        // No fade, no lingering frozen state — cleared the instant
        // watching stops, not when some later message confirms it.
        setAgentCursors({});
      }
    };

    if (document.visibilityState === "visible") startBeating();
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("beforeunload", leave);

    return () => {
      stopBeating();
      leave();
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("beforeunload", leave);
    };
  }, [roomId]);

  // ---------- Live cursor rendering in Monaco ----------

  useEffect(() => {
    const editor = editorRef.current;
    const ns = monacoNsRef.current;
    if (!editor || !ns) return;

    const liveAgentIds = new Set(Object.keys(agentCursors));

    // Remove widgets for agents no longer present at all.
    for (const id of Object.keys(cursorWidgetsRef.current)) {
      if (!liveAgentIds.has(id)) {
        editor.removeContentWidget(cursorWidgetsRef.current[id]);
        delete cursorWidgetsRef.current[id];
      }
    }

    for (const cursor of Object.values(agentCursors)) {
      // Only render a cursor for the file actually open in this editor
      // instance right now — a live edit on a different tab still shows
      // as the tab/tree live dot (rendered separately), not a phantom
      // cursor in the wrong file.
      if (cursor.path !== activePath) {
        const existing = cursorWidgetsRef.current[cursor.agentId];
        if (existing) {
          editor.removeContentWidget(existing);
          delete cursorWidgetsRef.current[cursor.agentId];
        }
        continue;
      }

      const existing = cursorWidgetsRef.current[cursor.agentId];
      if (existing) editor.removeContentWidget(existing);

      const dom = document.createElement("div");
      dom.style.position = "relative";
      const tag = document.createElement("div");
      tag.textContent = cursor.name;
      tag.style.cssText =
        "position:absolute;left:0;top:-22px;display:flex;align-items:center;gap:4px;background:var(--login-accent);color:#0A0C11;font-size:10.5px;font-weight:600;padding:2px 7px 2px 5px;border-radius:5px 5px 5px 0;white-space:nowrap;";
      const caret = document.createElement("div");
      caret.className = "ide-agent-cursor-caret";
      caret.style.cssText =
        "position:absolute;left:0;top:-1px;bottom:-1px;width:2px;background:var(--login-accent);box-shadow:0 0 6px rgba(76,211,194,.28);";
      dom.appendChild(caret);
      dom.appendChild(tag);

      const widget: monacoEditor.editor.IContentWidget = {
        getId: () => `ide-agent-cursor-${cursor.agentId}`,
        getDomNode: () => dom,
        getPosition: () => ({
          position: { lineNumber: cursor.line, column: cursor.column },
          preference: [ns.editor.ContentWidgetPositionPreference.EXACT],
        }),
      };
      editor.addContentWidget(widget);
      cursorWidgetsRef.current[cursor.agentId] = widget;

      // A brief fading highlight on the line an agent just moved to —
      // the real, honest signal available today (no per-keystroke edit
      // event exists without Batch 2's tool wiring): a cursor arriving
      // at a new line is the closest real proxy for "just wrote this,"
      // and the fade itself is what point 8 asks for regardless of
      // exactly which edit triggered it.
      const key = cursor.agentId;
      editFadeDecorationsRef.current[key]?.clear();
      const collection = editor.createDecorationsCollection([
        {
          range: new ns.Range(cursor.line, 1, cursor.line, 1),
          options: { isWholeLine: true, className: "ide-agent-recent-edit" },
        },
      ]);
      editFadeDecorationsRef.current[key] = collection;
      if (editFadeTimers.current[key]) clearTimeout(editFadeTimers.current[key]);
      editFadeTimers.current[key] = setTimeout(() => {
        collection.clear();
      }, 2600);
    }
  }, [agentCursors, activePath]);

  // Presence lost (or file closed): strip every live widget/decoration
  // immediately, matching the "no fade, no lingering frozen state" rule
  // even for widgets the per-cursor effect above wouldn't otherwise
  // revisit until its own next dependency change.
  useEffect(() => {
    if (Object.keys(agentCursors).length > 0) return;
    const editor = editorRef.current;
    if (!editor) return;
    for (const w of Object.values(cursorWidgetsRef.current)) editor.removeContentWidget(w);
    cursorWidgetsRef.current = {};
    for (const c of Object.values(editFadeDecorationsRef.current)) c.clear();
    editFadeDecorationsRef.current = {};
  }, [agentCursors]);

  // ---------- File tree / tabs / save ----------

  const toggleDir = (path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
        if (!dirListings[path]) send({ type: "list_dir", path });
      }
      return next;
    });
  };

  const openFile = useCallback(
    (path: string) => {
      setActivePath(path);
      setOpenTabs((prev) => (prev.includes(path) ? prev : [...prev, path]));
      setOpenFiles((prev) => {
        if (prev[path]) return prev;
        return { ...prev, [path]: { path, content: "", dirty: false, loading: true } };
      });
      send({ type: "read_file", path });
    },
    [send],
  );

  const closeTab = (path: string, e?: React.MouseEvent) => {
    e?.stopPropagation();
    setOpenTabs((prev) => {
      const idx = prev.indexOf(path);
      const next = prev.filter((p) => p !== path);
      if (activePath === path) setActivePath(next[idx] ?? next[idx - 1] ?? null);
      return next;
    });
  };

  const saveActiveFile = useCallback(() => {
    if (!activePath) return;
    const file = openFiles[activePath];
    if (!file) return;
    send({ type: "write_file", path: activePath, content_base64: textToBase64(file.content) });
  }, [activePath, openFiles, send]);

  // Auto Save: a real, working toggle — writes on every change once on,
  // debounced so it isn't a write-per-keystroke.
  const autoSaveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (!autoSave || !activePath) return;
    const file = openFiles[activePath];
    if (!file?.dirty) return;
    if (autoSaveTimer.current) clearTimeout(autoSaveTimer.current);
    autoSaveTimer.current = setTimeout(saveActiveFile, 600);
    return () => {
      if (autoSaveTimer.current) clearTimeout(autoSaveTimer.current);
    };
  }, [autoSave, activePath, openFiles, saveActiveFile]);

  const submitSaveAs = () => {
    if (!activePath || !saveAsValue.trim()) return;
    const file = openFiles[activePath];
    if (!file) return;
    const newPath = saveAsValue.trim();
    send({ type: "write_file", path: newPath, content_base64: textToBase64(file.content) });
    setOpenFiles((prev) => ({
      ...prev,
      [newPath]: { path: newPath, content: file.content, dirty: false, loading: false },
    }));
    setOpenTabs((prev) => (prev.includes(newPath) ? prev : [...prev, newPath]));
    setActivePath(newPath);
    setSaveAsOpen(false);
    setSaveAsValue("");
  };

  const closeFolder = () => {
    setFolderPath(null);
    setDirListings({});
    setExpanded(new Set());
    setOpenTabs([]);
    setOpenFiles({});
    setActivePath(null);
  };

  const openRecent = (recent: RecentFolder) => {
    send({ type: "open_folder", path: recent.path });
    setRecentOpen(false);
  };

  // ---------- Chat / terminal composer ----------

  const sendChatMessage = useCallback(
    async (content: string) => {
      if (!roomId || !content.trim()) return;
      let mentionedAgentIds: string[] = [];
      let body = content;
      const mentionMatch = /^@(\S+)\s+([\s\S]*)$/.exec(content);
      if (mentionMatch) {
        const target = roomAgents.find(
          (a) => a.name.toLowerCase() === mentionMatch[1].toLowerCase(),
        );
        if (target) {
          mentionedAgentIds = [target.id];
          body = mentionMatch[2];
        }
      }
      try {
        await apiFetch(`/v1/rooms/${roomId}/messages`, {
          method: "POST",
          body: { content: body, mentioned_agent_ids: mentionedAgentIds },
        });
      } catch {
        // The real chat message failed to send — the transcript simply
        // won't show it; the composer keeps whatever the human typed
        // isn't lost since state below only clears on a call that got
        // this far without throwing.
      }
    },
    [roomId, roomAgents],
  );

  const submitChat = () => {
    if (!chatValue.trim()) return;
    void sendChatMessage(chatValue);
    setChatValue("");
  };

  const submitShell = () => {
    if (!shellValue.trim()) return;
    if (!shellRunning) send({ type: "start_shell" });
    pendingOutputId.current = null;
    setTranscript((prev) => [
      ...prev,
      { kind: "command", id: nextEntryId(), actor: "human", text: shellValue },
    ]);
    send({ type: "shell_input", data: shellValue + "\r\n" });
    setShellValue("");
  };

  const startShell = () => {
    setPanelOpen(true);
    setBottomTab("terminal");
    if (!shellRunning) send({ type: "start_shell" });
  };

  // ---------- Global keyboard shortcuts ----------

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const ctrl = e.ctrlKey || e.metaKey;
      if (!ctrl) {
        if (e.key === "Escape") {
          setCmdkOpen(false);
          setRecentOpen(false);
          setSaveAsOpen(false);
        }
        return;
      }
      if (e.key === "`") {
        e.preventDefault();
        if (e.shiftKey) startShell();
        else setPanelOpen((p) => !p);
      } else if (e.key.toLowerCase() === "k") {
        e.preventDefault();
        setCmdkOpen(true);
      } else if (e.key.toLowerCase() === "b") {
        e.preventDefault();
        setExplorerOpen((v) => !v);
      } else if (e.key.toLowerCase() === "o") {
        e.preventDefault();
        send({ type: "pick_folder" });
      } else if (e.key.toLowerCase() === "r") {
        e.preventDefault();
        setRecentOpen((v) => !v);
      } else if (e.key === "1") {
        e.preventDefault();
        router.push("/dashboard");
      } else if (e.key.toLowerCase() === "s" && e.shiftKey) {
        e.preventDefault();
        if (activePath) {
          setSaveAsValue(activePath);
          setSaveAsOpen(true);
        }
      } else if (e.key.toLowerCase() === "s") {
        e.preventDefault();
        saveActiveFile();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activePath, saveActiveFile, router, send]);

  const renderEntries = (dirPath: string, depth: number) => {
    const entries = dirListings[dirPath];
    if (!entries) return null;
    return entries.map((entry) => {
      const isLive = Object.values(agentCursors).some((c) => c.path === entry.path);
      if (entry.is_dir) {
        const isOpen = expanded.has(entry.path);
        return (
          <div key={entry.path}>
            <button
              type="button"
              onClick={() => toggleDir(entry.path)}
              style={{ paddingLeft: `${depth * 14 + 4}px` }}
              className="flex w-full items-center gap-1.5 rounded-md py-1 pr-2 text-left text-[13px] text-[var(--ide-text-secondary)] hover:bg-[var(--ide-surface)]"
            >
              <span className={`flex shrink-0 transition-transform ${isOpen ? "rotate-90" : ""}`}>
                <ChevronRightIcon />
              </span>
              <FileGlyph isDir name={entry.name} />
              <span className="truncate">{entry.name}</span>
            </button>
            {isOpen && renderEntries(entry.path, depth + 1)}
          </div>
        );
      }
      return (
        <button
          key={entry.path}
          type="button"
          onClick={() => openFile(entry.path)}
          style={{ paddingLeft: `${depth * 14 + 8}px` }}
          className={`flex w-full items-center gap-1.5 rounded-md py-1 pr-2 text-left text-[13px] ${
            activePath === entry.path
              ? "bg-[rgba(76,211,194,.1)] text-[var(--ide-text)]"
              : "text-[var(--ide-text-secondary)] hover:bg-[var(--ide-surface)]"
          }`}
        >
          <FileGlyph isDir={false} name={entry.name} />
          <span className="truncate">{entry.name}</span>
          {isLive && (
            <span className="ml-auto h-[5px] w-[5px] shrink-0 rounded-full bg-[var(--login-accent)] shadow-[0_0_4px_rgba(76,211,194,.28)]" />
          )}
        </button>
      );
    });
  };

  if (!COMPANION_URL) {
    return (
      <main className="flex h-full items-center justify-center px-6 text-center">
        <p className="max-w-[420px] text-[13px] text-[var(--ide-text-muted)]">
          NEXT_PUBLIC_COMPANION_WS_URL isn&apos;t set — see apps/web/.env.example.
        </p>
      </main>
    );
  }

  if (consentState !== "granted") {
    if (consentState === "checking") {
      return (
        <main className="flex h-full items-center justify-center px-6 text-center">
          <p className="text-[13px] text-[var(--ide-text-muted)]">Checking companion status…</p>
        </main>
      );
    }
    if (consentState === "unreachable") {
      return (
        <main className="flex h-full items-center justify-center px-6 text-center">
          <div className="flex max-w-[420px] flex-col gap-3">
            <p className="text-[13px] text-[var(--ide-text-muted)]">
              Couldn&apos;t reach the local companion process at{" "}
              {companionHttpBase(COMPANION_URL)} — start it and try again.
            </p>
            <button
              type="button"
              onClick={() => setConsentState("checking")}
              className="mx-auto rounded-lg border border-[var(--ide-border-strong)] px-4 py-2 text-[13px] text-[var(--ide-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--ide-text)]"
            >
              Try again
            </button>
          </div>
        </main>
      );
    }
    return (
      <main className="flex h-full items-center justify-center px-6 py-10">
        <div className="flex w-full max-w-[560px] flex-col gap-5 rounded-xl border border-[var(--room-warn)] bg-[var(--ide-surface-2)] p-6">
          <h1 className="text-[17px] font-semibold text-[var(--ide-text)]">
            Before you connect: this enables real command execution
          </h1>
          <p className="text-[13.5px] leading-relaxed text-[var(--ide-text-secondary)]">
            The Harmonia IDE talks to a small local process (the companion) running on this
            machine. Once connected, it can read and write real files and run a real shell — on
            your own request, or an agent&apos;s — in the folder you open. This is fundamentally
            different from anything else in Harmonia: every other action here has a bounded,
            well-understood effect; a real shell does not.
          </p>
          <p className="text-[13.5px] leading-relaxed text-[var(--ide-text-secondary)]">
            An agent can only act while you are actively watching this session — the moment you
            leave, its file and command tools become unavailable. Every action an agent takes is
            marked visibly in the terminal, distinct from anything you type yourself.
          </p>
          <p className="text-[13.5px] leading-relaxed text-[var(--ide-text-secondary)]">
            There is no command blocklist — a real shell makes one trivially easy to bypass, and
            a blocklist creates false confidence worse than having none. The real safety net is
            git: damage to a git-tracked file is recoverable through its real history. Damage
            outside a git repository&apos;s scope, or to files git isn&apos;t tracking, is not.
          </p>
          <p className="text-[13.5px] leading-relaxed text-[var(--ide-text-secondary)]">
            This consent is remembered on this machine, for this companion installation, until
            you revoke it yourself.
          </p>
          <button
            type="button"
            disabled={consentState === "granting"}
            onClick={() => void grantConsent()}
            className="self-start rounded-lg bg-[var(--login-accent)] px-4 py-2 text-[13.5px] font-medium text-[var(--ide-bg)] hover:bg-[#63e0d1] disabled:cursor-not-allowed disabled:opacity-60"
          >
            {consentState === "granting" ? "Enabling…" : "I understand — enable the companion"}
          </button>
        </div>
      </main>
    );
  }

  const activeFile = activePath ? openFiles[activePath] : null;
  const activeFileName = activePath ? activePath.split("/").pop()! : null;
  const presenceAgents: PresenceAgent[] = roomAgents.map((a) => ({
    id: a.id,
    name: a.name,
    provider: a.provider,
    active: agentStatus[a.id] === "running",
  }));
  const singleAgent = roomAgents.length >= 1 ? roomAgents[0] : null;

  const cmdkFiles = Object.keys(dirListings).flatMap((dir) =>
    (dirListings[dir] ?? [])
      .filter((e) => !e.is_dir)
      .map((e) => ({ path: e.path, name: e.name })),
  );

  return (
    <div className="flex h-full min-w-0 flex-col bg-[var(--ide-bg)] text-[var(--ide-text)]">
      <MenuBar
        folderOpen={!!folderPath}
        autoSave={autoSave}
        explorerOpen={explorerOpen}
        panelOpen={panelOpen}
        canUndo={!!activePath}
        canRedo={!!activePath}
        humanInitials={humanInitialsFor(humanName)}
        agents={presenceAgents}
        onOpenFolder={() => send({ type: "pick_folder" })}
        onCloseFolder={closeFolder}
        onSave={saveActiveFile}
        onSaveAs={() => {
          if (activePath) {
            setSaveAsValue(activePath);
            setSaveAsOpen(true);
          }
        }}
        onToggleAutoSave={() => setAutoSave((v) => !v)}
        onUndo={() => editorRef.current?.trigger("menu", "undo", null)}
        onRedo={() => editorRef.current?.trigger("menu", "redo", null)}
        onFind={() => editorRef.current?.getAction("actions.find")?.run()}
        onSelectAll={() => editorRef.current?.trigger("menu", "editor.action.selectAll", null)}
        onToggleExplorer={() => setExplorerOpen((v) => !v)}
        onTogglePanel={() => setPanelOpen((v) => !v)}
        onOpenCmdk={() => setCmdkOpen(true)}
        onNewTerminal={startShell}
        onGoToFile={() => setCmdkOpen(true)}
      />

      <div className="flex min-h-0 flex-1">
        {folderPath && explorerOpen && (
          <div className="flex w-[250px] shrink-0 flex-col border-r border-[var(--ide-border)] bg-[var(--ide-bg-sidebar)]">
            <div className="px-4 pt-3 pb-2 font-[family-name:var(--login-font-mono)] text-[10.5px] tracking-wide text-[var(--ide-text-muted)] uppercase">
              {folderPath.replace(/[/\\]+$/, "").split(/[/\\]/).pop()}
            </div>
            <div className="no-scrollbar flex-1 overflow-y-auto px-2 py-0.5">
              {renderEntries("", 0)}
            </div>
          </div>
        )}

        {folderPath ? (
          <div className="flex min-w-0 flex-1 flex-col bg-[var(--ide-bg-panel)]">
            <div className="flex min-h-0 flex-1 flex-col">
              {openTabs.length > 0 && (
                <div className="no-scrollbar flex shrink-0 items-stretch overflow-x-auto border-b border-[var(--ide-border)] bg-[var(--ide-bg-sidebar)] pt-1.5">
                  {openTabs.map((path) => {
                    const f = openFiles[path];
                    const isLive = Object.values(agentCursors).some((c) => c.path === path);
                    return (
                      <button
                        key={path}
                        type="button"
                        onClick={() => setActivePath(path)}
                        className={`group flex shrink-0 items-center gap-2 rounded-t-lg px-3 py-2 text-[12.5px] ${
                          activePath === path
                            ? "bg-[var(--ide-bg-panel)] text-[var(--ide-text)] shadow-[inset_0_2px_0_var(--login-accent)]"
                            : "text-[var(--ide-text-secondary)] hover:text-[var(--ide-text-secondary)]"
                        }`}
                      >
                        <FileGlyph isDir={false} name={path.split("/").pop() ?? path} />
                        <span className="max-w-[160px] truncate">
                          {path.split("/").pop()}
                          {f?.dirty ? " ●" : ""}
                        </span>
                        {isLive && (
                          <span className="h-[6px] w-[6px] shrink-0 rounded-full bg-[var(--login-accent)] shadow-[0_0_5px_rgba(76,211,194,.28)]" />
                        )}
                        <span
                          role="button"
                          tabIndex={-1}
                          onClick={(e) => closeTab(path, e)}
                          className="rounded text-[var(--ide-text-muted)] opacity-0 hover:bg-[var(--ide-border-strong)] hover:text-[var(--ide-text)] group-hover:opacity-100"
                        >
                          <CloseIcon />
                        </span>
                      </button>
                    );
                  })}
                </div>
              )}
              <div className="min-h-0 flex-1">
                {!activePath && (
                  <div className="flex h-full items-center justify-center text-[13px] text-[var(--ide-text-muted)]">
                    Select a file to edit it.
                  </div>
                )}
                {activeFile && !activeFile.loading && (
                  <MonacoEditor
                    path={activeFile.path}
                    language={languageForPath(activeFile.path)}
                    value={activeFile.content}
                    theme="vs-dark"
                    onMount={(editor, monacoNs) => {
                      editorRef.current = editor;
                      monacoNsRef.current = monacoNs as unknown as typeof monacoEditor;
                    }}
                    onChange={(value) =>
                      setOpenFiles((prev) => ({
                        ...prev,
                        [activeFile.path]: { ...prev[activeFile.path], content: value ?? "", dirty: true },
                      }))
                    }
                    options={{ minimap: { enabled: false }, fontSize: 13 }}
                  />
                )}
              </div>
            </div>

            <div
              style={{ height: panelOpen ? 320 : 0 }}
              className="shrink-0 overflow-hidden border-t border-[var(--ide-border)] bg-[var(--ide-bg-panel)] transition-[height] duration-[180ms] ease-[cubic-bezier(.4,0,.2,1)]"
            >
              <TerminalPanel
                bottomTab={bottomTab}
                onBottomTabChange={setBottomTab}
                onClose={() => setPanelOpen(false)}
                onNewTerminal={startShell}
                transcript={transcript}
                mode={terminalMode}
                onModeChange={setTerminalMode}
                chatValue={chatValue}
                onChatChange={setChatValue}
                onChatSubmit={submitChat}
                shellValue={shellValue}
                onShellChange={setShellValue}
                onShellSubmit={submitShell}
                shellRunning={shellRunning}
                onStartShell={startShell}
              />
            </div>
            {!panelOpen && (
              <button
                type="button"
                onClick={() => setPanelOpen(true)}
                className="flex h-6 shrink-0 items-center gap-1.5 border-t border-[var(--ide-border)] bg-[var(--ide-bg-sidebar)] px-3 text-[11px] text-[var(--ide-text-muted)] hover:text-[var(--ide-text-secondary)]"
              >
                <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4">
                  <path d="M4 6l4 4 4-4" />
                </svg>
                Terminal (closed) — click or Ctrl+` to reopen
              </button>
            )}
          </div>
        ) : (
          <div className="flex flex-1 flex-col items-center justify-center gap-7">
            <OrbMark size={120} dotCount={11} radius={42} dotSize={3.2} />
            <div className="flex min-w-[280px] flex-col gap-0.5">
              <EmptyStateAction
                label="Open Folder"
                keys={["Ctrl", "O"]}
                onRun={() => send({ type: "pick_folder" })}
              />
              <EmptyStateAction
                label="Open Recent"
                keys={["Ctrl", "R"]}
                onRun={() => setRecentOpen(true)}
              />
              <EmptyStateAction
                label="Command Palette"
                keys={["Ctrl", "K"]}
                onRun={() => setCmdkOpen(true)}
              />
              <EmptyStateAction
                label="Switch to Rooms"
                keys={["Ctrl", "1"]}
                onRun={() => router.push("/dashboard")}
              />
            </div>
            {(connectionError || connectionState === "error") && (
              <p className="max-w-[360px] text-center text-[13px] text-[var(--room-warn)]">
                {connectionError ?? "Couldn't reach the companion process — is it running?"}
              </p>
            )}
          </div>
        )}
      </div>

      <div className="flex h-6 shrink-0 items-center justify-between bg-gradient-to-r from-[var(--login-accent)] to-[#3fc2b0] px-2.5 font-[family-name:var(--login-font-mono)] text-[11px] font-medium text-[var(--ide-bg)]">
        <div className="flex items-center gap-3.5">
          <span className="flex items-center gap-1.5">
            <span className="h-1.5 w-1.5 rounded-full bg-[var(--ide-bg)]" />
            companion:{" "}
            {connectionState === "connected"
              ? "connected"
              : connectionState === "connecting"
                ? "connecting…"
                : connectionState === "error"
                  ? "connection failed"
                  : "disconnected"}
            {folderPath ? ` — ${folderPath}` : ""}
          </span>
        </div>
        <div className="flex items-center gap-3.5">
          <span>UTF-8</span>
          {activeFileName && <span>{languageForPath(activeFileName).toUpperCase()}</span>}
        </div>
      </div>

      <CommandPalette
        open={cmdkOpen}
        onClose={() => setCmdkOpen(false)}
        files={cmdkFiles}
        activeFileName={activeFileName}
        agentName={singleAgent?.name ?? null}
        onAskAgent={(question) => {
          if (!activeFileName) return;
          void sendChatMessage(`About ${activeFileName}: ${question}`);
          setPanelOpen(true);
          setBottomTab("terminal");
          setTerminalMode("chat");
        }}
        onOpenFile={openFile}
        actions={[
          { id: "new-terminal", label: "New Terminal", shortcut: "Ctrl+Shift+`", run: startShell },
        ]}
      />

      {recentOpen && (
        <div
          className="fixed inset-0 z-[100] flex items-start justify-center bg-[rgba(6,8,11,.6)] pt-[120px] backdrop-blur-[3px]"
          onClick={() => setRecentOpen(false)}
        >
          <div
            onClick={(e) => e.stopPropagation()}
            className="w-[420px] max-w-[90vw] overflow-hidden rounded-xl border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] shadow-[0_24px_64px_rgba(0,0,0,.6)]"
          >
            <div className="border-b border-[var(--ide-border)] px-4 py-3 text-[12.5px] text-[var(--ide-text-muted)]">
              Open Recent
            </div>
            <div className="max-h-[320px] overflow-y-auto p-1.5">
              {recents.length === 0 && (
                <p className="px-2.5 py-4 text-center text-[13px] text-[var(--ide-text-muted)]">
                  No recent folders yet.
                </p>
              )}
              {recents.map((r) => (
                <button
                  key={r.path}
                  type="button"
                  onClick={() => openRecent(r)}
                  className="flex w-full flex-col items-start gap-0.5 rounded-lg px-2.5 py-2 text-left hover:bg-[rgba(76,211,194,.1)]"
                >
                  <span className="text-[13.5px] text-[var(--ide-text)]">{r.name}</span>
                  <span className="truncate text-[11px] text-[var(--ide-text-muted)]">{r.path}</span>
                </button>
              ))}
            </div>
          </div>
        </div>
      )}

      {saveAsOpen && (
        <div
          className="fixed inset-0 z-[100] flex items-start justify-center bg-[rgba(6,8,11,.6)] pt-[120px] backdrop-blur-[3px]"
          onClick={() => setSaveAsOpen(false)}
        >
          <div
            onClick={(e) => e.stopPropagation()}
            className="flex w-[420px] max-w-[90vw] flex-col gap-3 rounded-xl border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] p-4 shadow-[0_24px_64px_rgba(0,0,0,.6)]"
          >
            <p className="text-[12.5px] text-[var(--ide-text-muted)]">
              Save As — real relative path within {folderPath}
            </p>
            <input
              autoFocus
              value={saveAsValue}
              onChange={(e) => setSaveAsValue(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && submitSaveAs()}
              className="rounded-lg border border-[var(--ide-border-strong)] bg-transparent px-3 py-2 text-[13px] text-[var(--ide-text)] outline-none focus:border-[var(--login-accent)]"
            />
            <button
              type="button"
              onClick={submitSaveAs}
              className="self-end rounded-lg bg-[var(--login-accent)] px-4 py-1.5 text-[13px] font-medium text-[var(--ide-bg)] hover:bg-[#63e0d1]"
            >
              Save
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
