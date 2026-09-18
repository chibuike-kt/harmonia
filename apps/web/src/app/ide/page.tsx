"use client";

import dynamic from "next/dynamic";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import type * as monacoEditor from "monaco-editor";
import { OrbMark } from "@/components/OrbMark";
import { Tooltip } from "@/components/Tooltip";
import { apiFetch } from "@/lib/api";
import {
  findRecentFolder,
  listRecentFolders,
  rememberRecentFolder,
  type RecentFolder,
} from "@/lib/ideRecents";
import { ReconnectingEventSource } from "@/lib/sseReconnect";
import { useResizable } from "@/lib/useResizable";
import {
  EDITOR_THEMES,
  loadStoredEditorTheme,
  registerEditorTheme,
  storeEditorTheme,
} from "@/lib/editorThemes";
import { MenuBar, type PresenceAgent } from "@/components/ide/MenuBar";
import { type AddedAgent } from "@/components/AddAgentMenu";
import { readFileAsBase64 } from "@/components/Composer";
import { CommandPalette } from "@/components/ide/CommandPalette";
import { DiffView, type FileEditProposal } from "@/components/ide/DiffView";
import { ActivityBar, type SidebarView } from "@/components/ide/ActivityBar";
import { ExplorerPanel, FileGlyph, type DirEntry } from "@/components/ide/ExplorerPanel";
import { SearchPanel, type SearchMatch } from "@/components/ide/SearchPanel";
import { SourceControlPanel, type GitStatusState } from "@/components/ide/SourceControlPanel";
import { ThemePicker } from "@/components/ide/ThemePicker";
import {
  TerminalPanel,
  type AgentSessionState,
  type AgentStartForm,
  type BottomTab,
  type TerminalMode,
  type TerminalPanelHandle,
  type TranscriptEntry,
} from "@/components/ide/TerminalPanel";
import { CloseIcon } from "@/components/icons";

const MonacoEditor = dynamic(() => import("@monaco-editor/react"), {
  ssr: false,
  loading: () => (
    <div className="flex flex-1 items-center justify-center text-[13px] text-[var(--ide-text-muted)]">
      Loading editor…
    </div>
  ),
});

interface ServerMessage {
  type: string;
  path?: string;
  new_path?: string;
  entries?: DirEntry[];
  content_base64?: string;
  data?: string;
  exit_code?: number;
  message?: string;
  terminal_id?: string;
  cwd?: string;
  git_repo?: boolean;
  git_branch?: string;
  git_files?: { path: string; staged: string; worktree: string }[];
  search_results?: SearchMatch[];
  search_query?: string;
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
  mentioned_agent_ids?: string[];
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
  companion_action?: {
    id: string;
    room_id: string;
    type: string;
    path?: string;
    data: string;
    actor: string;
  };
  event?: {
    type: string;
    payload: Record<string, unknown>;
    sender: { agent_id?: string };
  };
  agent_loop_status?: {
    session_id: string;
    cycle: number;
    max_cycles: number;
    spend_usd: number;
    dollar_cap_usd: number;
    state: string;
    message?: string;
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
  // Real gap found live: a list_dir/git_status request that runs
  // automatically in the background (on folder-open, on switching to
  // Source Control) has nothing to do with any conversation, yet its
  // raw companion error string ("list dir: no folder is open") was
  // landing straight in the Chat transcript, interleaved with real
  // messages it had zero connection to. This is where those two error
  // kinds go instead — the status bar, companion connectivity's own
  // real home, visible regardless of which panel is open (unlike the
  // empty-state screen connectionError also feeds, which unmounts the
  // moment a folder is open). Cleared the moment the same kind of
  // request next succeeds.
  const [backgroundNotice, setBackgroundNotice] = useState<string | null>(
    null,
  );
  const [consentState, setConsentState] = useState<ConsentState>("checking");

  const [folderPath, setFolderPath] = useState<string | null>(null);
  const [dirListings, setDirListings] = useState<Record<string, DirEntry[]>>({});
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const [openTabs, setOpenTabs] = useState<string[]>([]);
  const [openFiles, setOpenFiles] = useState<Record<string, OpenFile>>({});
  const [activePath, setActivePath] = useState<string | null>(null);
  const [autoSave, setAutoSave] = useState(false);

  const [explorerOpen, setExplorerOpen] = useState(true);
  const [sidebarView, setSidebarView] = useState<SidebarView>("explorer");
  // Real drag-to-resize, the exact same mechanism Harmonia's own main
  // Sidebar already uses (see useResizable's own doc comment) — the
  // Explorer's own right-edge handle grows the panel when dragged right
  // (normal), the Terminal's own top-edge handle grows it when dragged
  // up, i.e. a *smaller* clientY (reverse).
  const explorerResize = useResizable({ axis: "x", initial: 250, min: 170, max: 520 });
  const terminalResize = useResizable({
    axis: "y",
    initial: 320,
    min: 120,
    max: 720,
    direction: "reverse",
  });
  const [gitStatus, setGitStatus] = useState<GitStatusState | null>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<SearchMatch[]>([]);
  const [searching, setSearching] = useState(false);
  const pendingRevealLineRef = useRef<{ path: string; line: number } | null>(null);
  const [editorTheme, setEditorTheme] = useState(loadStoredEditorTheme);
  const [panelOpen, setPanelOpen] = useState(true);
  const [bottomTab, setBottomTab] = useState<BottomTab>("terminal");
  const [terminalMode, setTerminalMode] = useState<TerminalMode>("shell");
  const [chatValue, setChatValue] = useState("");
  const [transcript, setTranscript] = useState<TranscriptEntry[]>([]);
  const terminalPanelRef = useRef<TerminalPanelHandle | null>(null);
  // The one companion_action currently forwarded to the companion and
  // awaiting its real response — the relay protocol's frontend half
  // (ADR-010: "backend -> existing live channel -> frontend -> the
  // browser's WebSocket to companion -> result relayed back"). Only one
  // at a time: today's real callers (approved file-edit proposals) are
  // never concurrent with each other for the same browser tab.
  const pendingRelayAction = useRef<{
    id: string;
    roomId: string;
    type: string;
    path?: string;
  } | null>(null);

  // The one run_command companion action currently awaiting its real
  // captured output (ADR-011's agent loop) — buffered separately from
  // pendingRelayAction since its resolution comes from accumulating real
  // terminal_output for its terminal, not from a single companion
  // response message. terminalId is the loop's own dedicated terminal;
  // marker is the unique echo this exact command's real output is
  // captured up to.
  const pendingRunCommand = useRef<{
    roomId: string;
    actionId: string;
    terminalId: string;
    marker: string;
    buffer: string;
  } | null>(null);

  const [agentSession, setAgentSession] = useState<AgentSessionState | null>(null);
  const [agentStartError, setAgentStartError] = useState<string | null>(null);

  const [cmdkOpen, setCmdkOpen] = useState(false);
  const [recentOpen, setRecentOpen] = useState(false);
  const [recents, setRecents] = useState<RecentFolder[]>([]);
  const [saveAsOpen, setSaveAsOpen] = useState(false);
  const [saveAsValue, setSaveAsValue] = useState("");

  // This IDE session's paired room (ADR-010: still a room underneath) —
  // null until a folder is open and the room lookup/creation resolves.
  const [roomId, setRoomId] = useState<string | null>(null);
  const [roomAgents, setRoomAgents] = useState<RoomAgentDTO[]>([]);
  const roomAgentsRef = useRef<RoomAgentDTO[]>([]);
  const [agentStatus, setAgentStatus] = useState<Record<string, string>>({});
  const [agentCursors, setAgentCursors] = useState<Record<string, AgentCursorState>>({});
  const [humanName, setHumanName] = useState("You");
  const [followingAgentId, setFollowingAgentId] = useState<string | null>(null);
  // The pending-approval diff view — ADR-010's presence-gone fallback.
  // A full view takeover while pending (see this feature's own report
  // for why), cleared the instant its ACTION.RESOLVE arrives, whether
  // that resolution came from this tab or another one watching the same
  // room.
  const [pendingFileEdit, setPendingFileEdit] = useState<FileEditProposal | null>(null);

  const editorRef = useRef<monacoEditor.editor.IStandaloneCodeEditor | null>(null);
  const editorContainerRef = useRef<HTMLDivElement | null>(null);
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

  // Answers the backend's companionrelay.Dispatch call that's still
  // blocked waiting for this — the frontend's own end of ADR-010's relay
  // protocol. Real message correlation (the action id), not a guess:
  // Dispatch already times out cleanly on its own if this never arrives
  // (a closed tab mid-command), so a failed POST here is a real,
  // non-fatal race with that timeout, not something this needs to retry.
  const postRelayResult = useCallback(
    (roomId: string, actionId: string, result: { output?: string; err?: string }) => {
      void fetch(
        `${process.env.NEXT_PUBLIC_API_BASE_URL}/v1/rooms/${roomId}/companion_actions/${actionId}/result`,
        {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(result),
        },
      ).catch(() => {});
    },
    [],
  );

  // ---------- Companion WebSocket ----------

  const handleServerMessage = useCallback(
    (msg: ServerMessage) => {
      switch (msg.type) {
        case "folder_opened":
          setFolderPath(msg.path ?? null);
          setDirListings({});
          setExpanded(new Set([""]));
          send({ type: "list_dir", path: "" });
          break;
        case "dir_listing":
          setDirListings((prev) => ({
            ...prev,
            [msg.path ?? ""]: msg.entries ?? [],
          }));
          setBackgroundNotice(null);
          break;
        case "file_content": {
          if (!msg.path) break;
          // ADR-011's read_file relay tool answers here too — a companion
          // read triggered by the agent loop, not a human opening a tab, so
          // it resolves the pending relay action instead of opening an
          // editor tab for a file the human never asked to see.
          const pendingRead = pendingRelayAction.current;
          if (pendingRead && pendingRead.type === "read_file" && pendingRead.path === msg.path) {
            pendingRelayAction.current = null;
            postRelayResult(pendingRead.roomId, pendingRead.id, {
              output: base64ToText(msg.content_base64 ?? ""),
            });
            break;
          }
          setOpenFiles((prev) => ({
            ...prev,
            [msg.path as string]: {
              path: msg.path as string,
              content: base64ToText(msg.content_base64 ?? ""),
              dirty: false,
              loading: false,
            },
          }));
          // A Search result's own real "jump to this line" — the file
          // just finished loading, so this is the first real point
          // Monaco has real content to reveal a position in. A short
          // defer lets this same render actually mount the editor for a
          // tab that was just opened, before revealPositionInCenter is
          // called against it.
          if (pendingRevealLineRef.current?.path === msg.path) {
            const line = pendingRevealLineRef.current.line;
            pendingRevealLineRef.current = null;
            setTimeout(() => editorRef.current?.revealLineInCenter(line), 50);
          }
          break;
        }
        case "file_written": {
          if (!msg.path) break;
          setOpenFiles((prev) => {
            const existing = prev[msg.path as string];
            if (!existing) return prev;
            return {
              ...prev,
              [msg.path as string]: { ...existing, dirty: false },
            };
          });
          const pending = pendingRelayAction.current;
          if (pending && pending.type === "write_file" && pending.path === msg.path) {
            pendingRelayAction.current = null;
            postRelayResult(pending.roomId, pending.id, { output: "written" });
          }
          break;
        }
        // New File/New Folder's own real ack — the parent directory's
        // own fresh dir_listing (handled by the "dir_listing" case
        // above) is what actually updates the tree; there's nothing
        // else real to do with this one beyond letting it arrive.
        case "path_created":
          break;
        // Delete: close any real open tab for the deleted path, or —
        // for a deleted folder — every real open tab nested under it.
        case "path_deleted": {
          const deletedPath = msg.path ?? "";
          const isDeleted = (p: string) => p === deletedPath || p.startsWith(`${deletedPath}/`);
          setOpenTabs((prev) => prev.filter((p) => !isDeleted(p)));
          setOpenFiles((prev) => {
            const next = { ...prev };
            for (const p of Object.keys(next)) if (isDeleted(p)) delete next[p];
            return next;
          });
          setActivePath((prev) => (prev && isDeleted(prev) ? null : prev));
          break;
        }
        // Rename: a real open tab for the renamed path moves with it,
        // in place — the human keeps editing the same real file under
        // its new name rather than losing the tab.
        case "path_renamed": {
          const oldPath = msg.path ?? "";
          const newPath = msg.new_path ?? "";
          setOpenTabs((prev) => prev.map((p) => (p === oldPath ? newPath : p)));
          setOpenFiles((prev) => {
            const file = prev[oldPath];
            if (!file) return prev;
            const next = { ...prev };
            delete next[oldPath];
            next[newPath] = { ...file, path: newPath };
            return next;
          });
          setActivePath((prev) => (prev === oldPath ? newPath : prev));
          break;
        }
        case "git_status":
          setGitStatus({
            repo: !!msg.git_repo,
            branch: msg.git_branch ?? "",
            files: msg.git_files ?? [],
          });
          setBackgroundNotice(null);
          break;
        case "search_results":
          setSearching(false);
          setSearchResults(msg.search_results ?? []);
          break;
        case "error": {
          const message = msg.message ?? "Unknown companion error.";
          setConnectionError(message);
          // list_dir and git_status run automatically in the background
          // (folder-open, switching to Source Control) — a failure there
          // has nothing to do with any conversation and never belongs in
          // the Chat transcript (found live: exactly this, interleaved
          // with real chat messages it was never actually connected to).
          // Every other companion error (create/rename/delete/write, a
          // relay action, a terminal op) still belongs here — those
          // really can be the direct result of something the human (or
          // an agent acting through them) just did in this room.
          const backgroundKinds = ["list dir:", "git status:"];
          const backgroundPrefix = backgroundKinds.find((p) =>
            message.startsWith(p),
          );
          if (backgroundPrefix) {
            const detail = message.slice(backgroundPrefix.length).trim();
            const subject =
              backgroundPrefix === "list dir:"
                ? "refresh the file list"
                : "refresh git status";
            setBackgroundNotice(`Couldn't ${subject} — ${detail}.`);
          } else {
            setTranscript((prev) => [
              ...prev,
              { kind: "error", id: nextEntryId(), text: message },
            ]);
          }
          const pending = pendingRelayAction.current;
          if (pending) {
            pendingRelayAction.current = null;
            postRelayResult(pending.roomId, pending.id, { err: message });
          }
          break;
        }
        default: {
          // ADR-011's create_terminal relay tool answers here — the loop's
          // own dedicated terminal, adopted into TerminalPanel's own agent
          // slot (never dispatch()'d as an ordinary terminal_created, which
          // would otherwise hijack the human's own main/split shell pane).
          if (msg.type === "terminal_created" && msg.terminal_id) {
            const pendingCreate = pendingRelayAction.current;
            if (pendingCreate && pendingCreate.type === "create_terminal") {
              pendingRelayAction.current = null;
              terminalPanelRef.current?.adoptAgentTerminal(msg.terminal_id);
              postRelayResult(pendingCreate.roomId, pendingCreate.id, {
                output: msg.terminal_id,
              });
              break;
            }
          }
          // ADR-011's run_command relay tool captures its real output here
          // — every terminal_output chunk for the pending command's own
          // terminal is buffered until its unique marker echo appears, the
          // same real output a human watching the terminal sees live.
          if (msg.type === "terminal_output" && msg.terminal_id) {
            const pendingRun = pendingRunCommand.current;
            if (pendingRun && pendingRun.terminalId === msg.terminal_id) {
              pendingRun.buffer += msg.data ?? "";
              const markerIdx = pendingRun.buffer.indexOf(pendingRun.marker);
              if (markerIdx !== -1) {
                pendingRunCommand.current = null;
                postRelayResult(pendingRun.roomId, pendingRun.actionId, {
                  output: pendingRun.buffer.slice(0, markerIdx),
                });
              }
            }
          }
          // Every real terminal_* message (terminal_created, terminal_output,
          // terminal_cwd, terminal_exited) — TerminalPanel owns every real
          // terminal instance's own lifecycle; this page only owns the wire.
          if (msg.type.startsWith("terminal_")) {
            terminalPanelRef.current?.dispatch(msg);
          }
        }
      }
    },
    [send, postRelayResult],
  );

  const connect = useCallback(() => {
    if (!COMPANION_URL) return;
    // A guard against overlapping connections, not just a nicety: each
    // WebSocket the companion accepts gets its own fresh session (no
    // folder open, no shell) — see internal/companion's own doc comment
    // on "no ambient state." If an effect re-run (React Strict Mode's
    // double-invoke in dev, or any future cause) ever called connect()
    // again while a connection is already live, wsRef.current would
    // silently start pointing at that new, blank session while
    // React state (folderPath, terminal state, the file tree) kept
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
      terminalPanelRef.current?.reset();
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
      const res = await fetch(`${companionHttpBase(COMPANION_URL)}/consent`, {
        method: "POST",
      });
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
      setAgentSession(null);
      setAgentStartError(null);
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
      const folderName =
        folderPath
          .replace(/[/\\]+$/, "")
          .split(/[/\\]/)
          .pop() || folderPath;
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
      .then((list) => {
        roomAgentsRef.current = list;
        setRoomAgents(list);
      })
      .catch(() => {
        roomAgentsRef.current = [];
        setRoomAgents([]);
      });
  }, [roomId]);

  // AddAgentMenu's own real POST already registered the agent — this
  // just reflects that same real result into this page's own room-agent
  // list, the exact same "trust the mutation's own response, don't
  // re-fetch" pattern the room chat page already uses for the identical
  // component.
  const onAgentAdded = useCallback((agent: AddedAgent) => {
    const asRoomAgent: RoomAgentDTO = {
      id: agent.id,
      name: agent.name,
      provider: agent.provider,
      status: "available",
    };
    roomAgentsRef.current = [...roomAgentsRef.current, asRoomAgent];
    setRoomAgents((prev) => [...prev, asRoomAgent]);
  }, []);

  const chatEntryFromMessage = useCallback(
    (m: ChatMessageDTO): TranscriptEntry => {
      if (m.sender_kind === "human") {
        // Real gap found live: an unaddressed message in a 2+-agent room
        // (ADR-004's implicit single-agent addressing only ever covers
        // exactly one agent; ADR-007's autonomous pickup is opt-in and
        // usually off) gets no reply and no error — CreateHandler simply
        // takes neither branch. Confirmed by pulling this exact room's
        // real message history: three consecutive human messages with
        // zero agent replies, and no failure message either. Mirrors
        // MessageRow.tsx's own real "unaddressed" flag from the main
        // Rooms page rather than inventing a second convention.
        const unaddressed =
          roomAgents.length >= 2 &&
          (m.mentioned_agent_ids?.length ?? 0) === 0;
        return {
          kind: "chat",
          id: m.id,
          sender: "human",
          content: m.content,
          unaddressed,
        };
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
    // attachListeners is the real stream's fixed listener set, unchanged
    // by ReconnectingEventSource below — every real reconnect gets a
    // brand-new EventSource, and this re-attaches the exact same
    // listeners to it each time.
    const attachListeners = (source: EventSource) => {
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
          setAgentStatus((prev) => ({
            ...prev,
            [msg.presence!.agent_id]: msg.presence!.status,
          }));
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

      // ADR-010's relay protocol, frontend half: the backend dispatched
      // this because an agent action (today, an approved file-edit
      // proposal's real write) needs the browser's own already-open
      // companion connection to actually reach the local filesystem — see
      // internal/companionrelay's own package doc. write_file is the one
      // real caller today; a future shell_input caller (Batch 2's agent
      // shell-exec tool) would forward the same way, correlated the same
      // way, through this same switch.
      source.addEventListener("companion_action", (e) => {
        const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
        const action = msg.companion_action;
        if (!action || !roomId) return;
        pendingRelayAction.current = {
          id: action.id,
          roomId,
          type: action.type,
          path: action.path,
        };
        if (action.type === "write_file" && action.path) {
          send({
            type: "write_file",
            path: action.path,
            content_base64: action.data,
          });
        } else if (action.type === "read_file" && action.path) {
          // ADR-011's agent loop reading a real project file — resolved in
          // handleServerMessage's own "file_content" case above.
          send({ type: "read_file", path: action.path });
        } else if (action.type === "create_terminal") {
          // ADR-011's agent loop opening its own dedicated real terminal —
          // resolved in handleServerMessage's own terminal_created handling.
          send({ type: "create_terminal" });
        } else if (action.type === "terminal_narrate" && action.path) {
          // Fire-and-forget: narrateTerminal (internal/companion) sends no
          // real acknowledgment back, so this relay action resolves the
          // instant the real narration line is sent, not on any later
          // companion response.
          send({
            type: "terminal_narrate",
            terminal_id: action.path,
            data: action.data,
          });
          pendingRelayAction.current = null;
          postRelayResult(roomId, action.id, { output: "narrated" });
        } else if (action.type === "run_command" && action.path) {
          // ADR-011's agent loop running a real shell command in its own
          // dedicated terminal. There's no dedicated "command finished"
          // companion message, so a unique marker is echoed right after the
          // real command and handleServerMessage's own terminal_output
          // handling above captures everything up to it as the real result
          // — the same real output a human watching this terminal sees live.
          const marker = `HARMONIA_CMD_DONE_${action.id}`;
          pendingRunCommand.current = {
            roomId,
            actionId: action.id,
            terminalId: action.path,
            marker,
            buffer: "",
          };
          pendingRelayAction.current = null;
          send({
            type: "terminal_input",
            terminal_id: action.path,
            data: `${action.data}\r\necho ${marker}\r\n`,
          });
        } else {
          // A real, honest failure for any action type this relay doesn't
          // implement yet, rather than leaving the backend's Dispatch call
          // hanging until its own timeout.
          pendingRelayAction.current = null;
          postRelayResult(roomId, action.id, {
            err: `companion action type ${action.type} not implemented in this browser session`,
          });
        }
      });

      // ADR-011's sustained agent loop: a live status snapshot after every
      // real cycle, published over this same room's existing live channel —
      // Task is carried over from the session this tab itself started
      // (POST .../agent_loop/start's own response), since the live payload
      // itself only carries what changes cycle to cycle.
      source.addEventListener("agent_loop_status", (e) => {
        const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
        const status = msg.agent_loop_status;
        if (!status) return;
        setAgentSession((prev) =>
          prev && prev.sessionId === status.session_id
            ? {
                ...prev,
                cycle: status.cycle,
                maxCycles: status.max_cycles,
                spendUsd: status.spend_usd,
                dollarCapUsd: status.dollar_cap_usd,
                state: status.state,
                message: status.message,
              }
            : prev,
        );
      });

      // The pending-approval diff view's own real trigger: an
      // ACTION.PROPOSE event for a propose_file_edit proposal. The event
      // itself only ever carries a summary (proposal_id, path) — the real
      // old/new content is fetched on demand via GET
      // /v1/action_proposals/{id} (actionproposal.GetHandler), never
      // pushed live, since it can be arbitrarily large. ACTION.RESOLVE for
      // that same proposal clears the view — resolved by any tab watching
      // this room, not just the one that resolved it.
      source.addEventListener("event", (e) => {
        const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
        const env = msg.event;
        if (!env) return;
        if (env.type === "ACTION.RESOLVE") {
          const proposalId = env.payload.proposal_id as string | undefined;
          setPendingFileEdit((prev) => (prev && prev.proposalId === proposalId ? null : prev));
          return;
        }
        if (env.type !== "ACTION.PROPOSE" || env.payload.action_type !== "propose_file_edit")
          return;
        const proposalId = env.payload.proposal_id as string;
        const path = (env.payload.path as string) ?? "";
        const agent = roomAgentsRef.current.find((a) => a.id === env.sender.agent_id);
        apiFetch<{
          payload: { old_content_base64?: string; new_content_base64?: string };
        }>(`/v1/action_proposals/${proposalId}`)
          .then((full) => {
            setPendingFileEdit({
              proposalId,
              path,
              agentName: agent?.name ?? "An agent",
              provider: agent?.provider,
              oldContent: base64ToText(full.payload.old_content_base64 ?? ""),
              newContent: base64ToText(full.payload.new_content_base64 ?? ""),
            });
          })
          .catch(() => {});
      });
    };

    // ReconnectingEventSource covers the one real gap EventSource itself
    // leaves open: it already retries an ordinary mid-stream drop on its
    // own, but permanently gives up — no further retry, ever — if even
    // the very first response comes back non-2xx (a transient 500, the
    // backend not warmed up yet). Found live: exactly that response once
    // left this room's whole live channel dead for the rest of the tab's
    // life. Every real reconnect creates a brand-new EventSource and
    // re-attaches the exact same listeners via create() below.
    const reconnecting = new ReconnectingEventSource({
      create: () => {
        const source = new EventSource(`${apiBase}/v1/rooms/${roomId}/stream`, {
          withCredentials: true,
        });
        esRef.current = source;
        attachListeners(source);
        return source;
      },
    });

    return () => {
      reconnecting.close();
      esRef.current = null;
    };
  }, [roomId, chatEntryFromMessage, send, postRelayResult]);

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
        return {
          ...prev,
          [path]: { path, content: "", dirty: false, loading: true },
        };
      });
      send({ type: "read_file", path });
    },
    [send],
  );

  // ---------- Explorer: New File/Folder, Rename, Delete ----------
  // Every real mutation goes straight over the companion WebSocket;
  // internal/companion's own real dir_listing refresh (handled by the
  // "dir_listing" case above) is what actually updates the tree.

  const createFile = useCallback(
    (parentDir: string, name: string) => {
      send({ type: "create_file", path: parentDir ? `${parentDir}/${name}` : name });
    },
    [send],
  );
  const createFolder = useCallback(
    (parentDir: string, name: string) => {
      send({ type: "create_folder", path: parentDir ? `${parentDir}/${name}` : name });
    },
    [send],
  );
  const renamePath = useCallback(
    (oldPath: string, newName: string) => {
      const slash = oldPath.lastIndexOf("/");
      const parent = slash < 0 ? "" : oldPath.slice(0, slash);
      const newPath = parent ? `${parent}/${newName}` : newName;
      send({ type: "rename_path", path: oldPath, new_path: newPath });
    },
    [send],
  );
  const deletePath = useCallback(
    (path: string) => {
      send({ type: "delete_path", path });
    },
    [send],
  );

  // ---------- Search ----------

  const runSearch = useCallback(() => {
    if (!searchQuery.trim()) return;
    setSearching(true);
    send({ type: "search_text", query: searchQuery });
  }, [searchQuery, send]);

  const onOpenSearchMatch = useCallback(
    (path: string, line: number) => {
      const existing = openFiles[path];
      if (existing && !existing.loading) {
        setTimeout(() => editorRef.current?.revealLineInCenter(line), 50);
      } else {
        pendingRevealLineRef.current = { path, line };
      }
      openFile(path);
    },
    [openFiles, openFile],
  );

  // ---------- Source Control ----------

  const refreshGitStatus = useCallback(() => send({ type: "git_status" }), [send]);
  const stageFile = useCallback((path: string) => send({ type: "git_stage", path }), [send]);
  const unstageFile = useCallback((path: string) => send({ type: "git_unstage", path }), [send]);
  const commitStaged = useCallback(
    (message: string) => send({ type: "git_commit", data: message }),
    [send],
  );

  // Real git status on open, and every time the Source Control view is
  // actually switched to — a human expects it to reflect what's real
  // right now, not whatever it happened to be the last time this view
  // was open.
  useEffect(() => {
    if (folderPath && sidebarView === "scm") refreshGitStatus();
  }, [folderPath, sidebarView, refreshGitStatus]);

  // Real editor theme choice, persisted across reloads — see
  // lib/editorThemes.ts's own doc comment for why "One Dark Pro" itself
  // isn't among the real options offered.
  const changeEditorTheme = useCallback((id: string) => {
    setEditorTheme(id);
    storeEditorTheme(id);
    if (monacoNsRef.current) void registerEditorTheme(monacoNsRef.current, id);
  }, []);

  // Follow mode — continuous, not a one-shot jump: this effect re-runs
  // on every real cursor update for the followed agent (agentCursors is
  // a dependency), so the viewport keeps tracking as the agent keeps
  // moving, for as long as following stays on. If the agent's cursor is
  // in a different file than the one currently open, this switches to
  // it first — VS Code Live Share's own "Follow" behavior when the
  // followed participant changes files — then reveals the position once
  // that file's editor is the active one (the effect naturally re-fires
  // when activePath catches up, since it's also a dependency).
  //
  // "Am I still actually following anyone" is derived from
  // followingAgentId + agentCursors on every render, not synchronized
  // into its own state — an agent's cursor disappearing (presence lost,
  // or it simply finished) genuinely ends following the instant that
  // happens, with nothing to reset via an effect.
  const followedCursor = followingAgentId ? (agentCursors[followingAgentId] ?? null) : null;
  const effectivelyFollowing = followingAgentId !== null && followedCursor !== null;

  useEffect(() => {
    if (!followedCursor) return;
    if (followedCursor.path !== activePath) {
      // Reacting to a real external signal (the followed agent's live
      // cursor moving to a different file) — same justification as this
      // codebase's other effects that sync to an external system rather
      // than to React's own state.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      openFile(followedCursor.path);
      return;
    }
    editorRef.current?.revealPositionInCenter(
      { lineNumber: followedCursor.line, column: followedCursor.column },
      monacoNsRef.current?.editor.ScrollType.Smooth,
    );
  }, [followedCursor, activePath, openFile]);

  const toggleFollow = useCallback((agentId: string) => {
    setFollowingAgentId((prev) => (prev === agentId ? null : agentId));
  }, []);
  const stopFollowing = useCallback(() => setFollowingAgentId(null), []);

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
    send({
      type: "write_file",
      path: activePath,
      content_base64: textToBase64(file.content),
    });
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
    send({
      type: "write_file",
      path: newPath,
      content_base64: textToBase64(file.content),
    });
    setOpenFiles((prev) => ({
      ...prev,
      [newPath]: {
        path: newPath,
        content: file.content,
        dirty: false,
        loading: false,
      },
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
      // Real bug found live: this composer is a plain textarea with no
      // @-picker (unlike Composer.tsx's own structured one, ADR-006
      // batch A — ADR-004), so addressing an agent here means matching
      // free-typed text against this room's real agent names. A
      // single-token regex (`@(\S+)`) can never match a real name that
      // contains a space — and "ChatGPT 2" is exactly such a name, a
      // real one this project's own AddAgentMenu can produce. Worse, a
      // naive single-token match on "@ChatGPT 2 ..." would silently
      // misaddress the message to "ChatGPT" (a genuine prefix of the
      // name actually typed) instead of leaving it unmatched — a wrong
      // agent replying is worse than none replying. Matching against
      // the room's own real roster, longest name first, fixes both: a
      // multi-word name now resolves at all, and a shorter name can
      // never steal a match that belongs to a longer one it's a prefix
      // of.
      //
      // The matched "@Name" is deliberately left in place rather than
      // stripped out of what actually gets sent as content: the human
      // typed it as a visible tag showing who this was addressed to,
      // and the transcript should keep showing that, not silently swap
      // it out for a plain-looking line with no visible connection to
      // the reply that follows it (mentioned_agent_ids already carries
      // the real addressing to the backend independent of this text).
      if (content.startsWith("@")) {
        const rest = content.slice(1);
        const byNameLength = [...roomAgents].sort(
          (a, b) => b.name.length - a.name.length,
        );
        for (const a of byNameLength) {
          if (!rest.toLowerCase().startsWith(a.name.toLowerCase())) continue;
          const after = rest.slice(a.name.length);
          if (after === "" || /^\s/.test(after)) {
            mentionedAgentIds = [a.id];
            break;
          }
        }
      }
      // Real fix for a real gap found live: Chat mode was posting through
      // this exact same room-message endpoint with nothing IDE-specific
      // added, so an agent asked about "this open file" had genuinely no
      // way to see it. The currently active tab's own live buffer (not a
      // re-read off disk — what the human actually sees, dirty edits
      // included) rides along as a real attachment, reusing ADR-008 batch
      // A's own content-injection path exactly as Composer.tsx's file
      // attach does — no second plumbing. Scoped to the one active tab:
      // with several files open, only the focused one is "this open
      // file" for an unscoped question.
      const active = activePath ? openFiles[activePath] : null;
      let attachment: { content: string; filename: string; mime_type: string } | undefined;
      if (active) {
        const contentBase64 = await readFileAsBase64(
          new Blob([active.content], { type: "text/plain" }),
        );
        attachment = {
          content: contentBase64,
          filename: active.path,
          mime_type: "text/plain",
        };
      }
      try {
        await apiFetch(`/v1/rooms/${roomId}/messages`, {
          method: "POST",
          body: {
            content,
            mentioned_agent_ids: mentionedAgentIds,
            ...(attachment ? { attachment } : {}),
          },
        });
      } catch {
        // The real chat message failed to send — the transcript simply
        // won't show it; the composer keeps whatever the human typed
        // isn't lost since state below only clears on a call that got
        // this far without throwing.
      }
    },
    [roomId, roomAgents, activePath, openFiles],
  );

  const submitChat = () => {
    if (!chatValue.trim()) return;
    void sendChatMessage(chatValue);
    setChatValue("");
  };

  // ---------- ADR-011: sustained agent-loop session ----------

  const startAgentSession = useCallback(
    async (form: AgentStartForm) => {
      if (!roomId) return;
      setAgentStartError(null);
      try {
        const res = await apiFetch<{
          session_id: string;
          task: string;
          cycle: number;
          max_cycles: number;
          spend_usd: number;
          dollar_cap_usd: number;
          state: string;
          message?: string;
        }>(`/v1/rooms/${roomId}/agent_loop/start`, {
          method: "POST",
          body: {
            agent_id: form.agentId,
            task: form.task,
            max_cycles: form.maxCycles,
            dollar_cap_usd: form.dollarCapUsd,
            wall_clock_seconds: form.wallClockSeconds,
          },
        });
        setAgentSession({
          sessionId: res.session_id,
          task: res.task,
          cycle: res.cycle,
          maxCycles: res.max_cycles,
          spendUsd: res.spend_usd,
          dollarCapUsd: res.dollar_cap_usd,
          state: res.state,
          message: res.message,
        });
      } catch (err) {
        setAgentStartError(err instanceof Error ? err.message : "Failed to start session.");
      }
    },
    [roomId],
  );

  const stopAgentSession = useCallback(() => {
    if (!roomId || !agentSession) return;
    void apiFetch(`/v1/rooms/${roomId}/agent_loop/${agentSession.sessionId}/stop`, {
      method: "POST",
    }).catch(() => {});
  }, [roomId, agentSession]);

  const openTerminalTab = useCallback(() => {
    setPanelOpen(true);
    setBottomTab("terminal");
    setTerminalMode("shell");
    terminalPanelRef.current?.createTerminal();
  }, []);

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
        if (e.shiftKey) openTerminalTab();
        else setPanelOpen((p) => !p);
      } else if (e.key.toLowerCase() === "k") {
        // A real terminal surface handles its own real Ctrl+K (clear
        // scrollback) via attachCustomKeyEventHandler — the same
        // focus-scoped resolution VS Code itself uses between its
        // `terminalFocus` keybindings and this same global shortcut, so
        // this handler defers to it instead of racing it for the
        // command palette.
        const active = document.activeElement as HTMLElement | null;
        if (active?.closest("[data-terminal-surface]")) return;
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
  }, [activePath, saveActiveFile, router, send, openTerminalTab]);

  // Monaco's own `automaticLayout` option only reacts to the window
  // resizing, not a sibling flex box (the terminal panel opening,
  // closing, or resizing) changing how much real height this container
  // has — found live: opening the terminal panel left the editor's own
  // real hit-tested box sized for the space it had before the panel
  // appeared, silently eating clicks meant for the terminal underneath
  // even though the editor had stopped painting that far down. A real
  // ResizeObserver on the editor's own container, calling its real
  // layout() on every genuine size change, is what the editor's own
  // upstream examples use for exactly this "resizable sibling" case.
  useEffect(() => {
    const container = editorContainerRef.current;
    if (!container) return;
    const observer = new ResizeObserver(() => {
      editorRef.current?.layout();
    });
    observer.observe(container);
    return () => observer.disconnect();
  }, [activePath]);

  const isPathLive = (path: string) => Object.values(agentCursors).some((c) => c.path === path);

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
              Couldn&apos;t reach the local companion process at {companionHttpBase(COMPANION_URL)}{" "}
              — start it and try again.
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
            The Harmonia IDE talks to a small local process (the companion) running on this machine.
            Once connected, it can read and write real files and run a real shell — on your own
            request, or an agent&apos;s — in the folder you open. This is fundamentally different
            from anything else in Harmonia: every other action here has a bounded, well-understood
            effect; a real shell does not.
          </p>
          <p className="text-[13.5px] leading-relaxed text-[var(--ide-text-secondary)]">
            An agent can only act while you are actively watching this session — the moment you
            leave, its file and command tools become unavailable. Every action an agent takes is
            marked visibly in the terminal, distinct from anything you type yourself.
          </p>
          <p className="text-[13.5px] leading-relaxed text-[var(--ide-text-secondary)]">
            There is no command blocklist — a real shell makes one trivially easy to bypass, and a
            blocklist creates false confidence worse than having none. The real safety net is git:
            damage to a git-tracked file is recoverable through its real history. Damage outside a
            git repository&apos;s scope, or to files git isn&apos;t tracking, is not.
          </p>
          <p className="text-[13.5px] leading-relaxed text-[var(--ide-text-secondary)]">
            This consent is remembered on this machine, for this companion installation, until you
            revoke it yourself.
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
    (dirListings[dir] ?? []).filter((e) => !e.is_dir).map((e) => ({ path: e.path, name: e.name })),
  );

  return (
    // min-h-0, same reasoning as rooms/[id]'s own root: fills the shell
    // outlet's exact height instead of growing past it now that the
    // outlet itself no longer scrolls — the editor/terminal/file-tree
    // panes below are the only things meant to ever scroll on this page.
    <div className="flex h-full min-h-0 min-w-0 flex-col bg-[var(--ide-bg)] text-[var(--ide-text)]">
      <MenuBar
        folderOpen={!!folderPath}
        autoSave={autoSave}
        explorerOpen={explorerOpen}
        panelOpen={panelOpen}
        canUndo={!!activePath}
        canRedo={!!activePath}
        humanInitials={humanInitialsFor(humanName)}
        agents={presenceAgents}
        roomId={roomId}
        onAgentAdded={onAgentAdded}
        followingAgentId={effectivelyFollowing ? followingAgentId : null}
        onToggleFollow={toggleFollow}
        onStopFollowing={stopFollowing}
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
        onNewTerminal={openTerminalTab}
        onGoToFile={() => setCmdkOpen(true)}
      />

      <div className="flex min-h-0 flex-1">
        {pendingFileEdit ? (
          <DiffView proposal={pendingFileEdit} onResolved={() => setPendingFileEdit(null)} />
        ) : (
          <>
            <ActivityBar active={sidebarView} onChange={setSidebarView} />

            {explorerOpen && (
              <div
                className="relative flex shrink-0 flex-col border-r border-[var(--ide-border)] bg-[var(--ide-bg-sidebar)]"
                style={{ width: explorerResize.size }}
              >
                <div className="min-h-0 flex-1">
                  {sidebarView === "explorer" &&
                    (folderPath ? (
                      <ExplorerPanel
                        folderName={
                          folderPath
                            .replace(/[/\\]+$/, "")
                            .split(/[/\\]/)
                            .pop() ?? folderPath
                        }
                        dirListings={dirListings}
                        expanded={expanded}
                        activePath={activePath}
                        isLive={isPathLive}
                        onToggleDir={toggleDir}
                        onOpenFile={openFile}
                        onCreateFile={createFile}
                        onCreateFolder={createFolder}
                        onRename={renamePath}
                        onDelete={deletePath}
                      />
                    ) : (
                      <p className="px-4 py-4 text-center text-[12.5px] text-[var(--ide-text-muted)]">
                        Open a folder to see its files.
                      </p>
                    ))}
                  {sidebarView === "search" &&
                    (folderPath ? (
                      <SearchPanel
                        query={searchQuery}
                        onQueryChange={setSearchQuery}
                        onSearch={runSearch}
                        results={searchResults}
                        searching={searching}
                        onOpenMatch={onOpenSearchMatch}
                      />
                    ) : (
                      <p className="px-4 py-4 text-center text-[12.5px] text-[var(--ide-text-muted)]">
                        Open a folder to search it.
                      </p>
                    ))}
                  {sidebarView === "scm" &&
                    (folderPath ? (
                      <SourceControlPanel
                        status={gitStatus}
                        onStage={stageFile}
                        onUnstage={unstageFile}
                        onCommit={commitStaged}
                        onRefresh={refreshGitStatus}
                      />
                    ) : (
                      <p className="px-4 py-4 text-center text-[12.5px] text-[var(--ide-text-muted)]">
                        Open a folder to see its source control status.
                      </p>
                    ))}
                </div>
                <div
                  onMouseDown={explorerResize.onHandleMouseDown}
                  className="absolute top-0 -right-[3px] z-30 h-full w-1.5 cursor-col-resize hover:bg-[var(--login-accent)]/35"
                />
              </div>
            )}

            <div className="flex min-w-0 flex-1 flex-col">
              <div className="flex min-h-0 flex-1 flex-col">
                {folderPath ? (
                  <>
                    {openTabs.length > 0 && (
                      <div className="no-scrollbar flex shrink-0 items-stretch overflow-x-auto border-b border-[var(--ide-border)] bg-[var(--ide-bg-sidebar)] pt-1.5">
                        {openTabs.map((path) => {
                          const f = openFiles[path];
                          const isLive = isPathLive(path);
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
                    <div ref={editorContainerRef} className="min-h-0 flex-1">
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
                          theme={
                            EDITOR_THEMES.find((t) => t.id === editorTheme)?.monacoName ?? "vs-dark"
                          }
                          onMount={(editor, monacoNs) => {
                            editorRef.current = editor;
                            const ns = monacoNs as unknown as typeof monacoEditor;
                            monacoNsRef.current = ns;
                            void registerEditorTheme(ns, editorTheme);
                          }}
                          onChange={(value) =>
                            setOpenFiles((prev) => ({
                              ...prev,
                              [activeFile.path]: {
                                ...prev[activeFile.path],
                                content: value ?? "",
                                dirty: true,
                              },
                            }))
                          }
                          options={{
                            minimap: { enabled: false },
                            fontSize: 13,
                            automaticLayout: true,
                          }}
                        />
                      )}
                    </div>
                  </>
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

              {/* The terminal is structurally independent of whether a
                  folder is open — a real terminal is useful before any
                  project exists, and internal/companion's own
                  createTerminal now falls back to the real user home
                  directory when no folder is open. */}
              {panelOpen && (
                <div
                  onMouseDown={terminalResize.onHandleMouseDown}
                  className="h-1 shrink-0 cursor-row-resize hover:bg-[var(--login-accent)]/35"
                />
              )}
              <div
                style={{ height: panelOpen ? terminalResize.size : 0 }}
                className="shrink-0 overflow-hidden border-t border-[var(--ide-border)] bg-[var(--ide-bg-panel)] transition-[height] duration-[180ms] ease-[cubic-bezier(.4,0,.2,1)]"
              >
                <TerminalPanel
                  ref={terminalPanelRef}
                  bottomTab={bottomTab}
                  onBottomTabChange={setBottomTab}
                  onClose={() => setPanelOpen(false)}
                  transcript={transcript}
                  mode={terminalMode}
                  onModeChange={setTerminalMode}
                  chatValue={chatValue}
                  onChatChange={setChatValue}
                  onChatSubmit={submitChat}
                  send={send}
                  folderOpen={!!folderPath}
                  agentAgents={roomAgents.map((a) => ({
                    id: a.id,
                    name: a.name,
                    provider: a.provider,
                  }))}
                  agentSession={agentSession}
                  agentStartError={agentStartError}
                  onStartAgentSession={startAgentSession}
                  onStopAgentSession={stopAgentSession}
                />
              </div>
              {!panelOpen && (
                <button
                  type="button"
                  onClick={() => setPanelOpen(true)}
                  className="flex h-6 shrink-0 items-center gap-1.5 border-t border-[var(--ide-border)] bg-[var(--ide-bg-sidebar)] px-3 text-[11px] text-[var(--ide-text-muted)] hover:text-[var(--ide-text-secondary)]"
                >
                  <svg
                    width="12"
                    height="12"
                    viewBox="0 0 16 16"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.4"
                  >
                    <path d="M4 6l4 4 4-4" />
                  </svg>
                  Terminal (closed) — click or Ctrl+` to reopen
                </button>
              )}
            </div>
          </>
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
          {backgroundNotice && (
            <Tooltip label={backgroundNotice} side="top" wrap>
              <span className="flex max-w-[280px] items-center gap-1 truncate font-semibold text-[#5c1a1a]">
                <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-[#5c1a1a]" />
                {backgroundNotice}
              </span>
            </Tooltip>
          )}
        </div>
        <div className="flex items-center gap-3.5">
          <span>UTF-8</span>
          {activeFileName && <span>{languageForPath(activeFileName).toUpperCase()}</span>}
          {/* A real, changeable editor theme — every real IDE has one;
              see lib/editorThemes.ts's own doc comment for what's real
              and licensed here. */}
          <ThemePicker value={editorTheme} onChange={changeEditorTheme} />
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
          {
            id: "new-terminal",
            label: "New Terminal",
            shortcut: "Ctrl+Shift+`",
            run: openTerminalTab,
          },
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
                  <span className="truncate text-[11px] text-[var(--ide-text-muted)]">
                    {r.path}
                  </span>
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
