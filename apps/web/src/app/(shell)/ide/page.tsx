"use client";

import dynamic from "next/dynamic";
import { useEffect, useRef, useState } from "react";
import "@xterm/xterm/css/xterm.css";
import {
  ChevronRightIcon,
  CloseIcon,
  FileIcon,
  FolderIcon,
} from "@/components/icons";

// Monaco touches `window` at import time — dynamic()+ssr:false is
// Next.js's own real mechanism for a browser-only component, not a
// workaround; a server-rendered <Editor> would crash the build outright.
const MonacoEditor = dynamic(() => import("@monaco-editor/react"), {
  ssr: false,
  loading: () => (
    <div className="flex flex-1 items-center justify-center text-[13px] text-[var(--login-text-muted)]">
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

type BottomTab = "problems" | "output" | "terminal";

type ConsentState =
  | "checking"
  | "required"
  | "granting"
  | "granted"
  | "unreachable";

const COMPANION_URL = process.env.NEXT_PUBLIC_COMPANION_WS_URL;

// The companion serves its plain-HTTP consent endpoint on the same host
// and port as its WebSocket — ws(s):// swapped for http(s):// with the
// trailing /ws dropped, never a second, independently configured URL
// that could quietly point somewhere else.
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

/**
 * The standalone Harmonia IDE — a real local project (opened via the
 * local companion process, cmd/companion), a real file tree, a real
 * multi-tab editor, a real terminal running real commands against it.
 * VS Code's own real structural layout (explorer / tabbed editor /
 * tabbed bottom panel / status bar), not its full chrome — no source
 * control, extensions, or remote icon rail, since none of that reflects
 * anything real here yet. Explicitly not a view of a chat room: no
 * room, no rename, nothing this page shares with RoomViewPage. Agent
 * conversation inside the terminal (typing "@agentname ..."
 * mid-session) is the next real slice, not built yet — this proves the
 * local IDE loop itself first: open, browse, edit, save, run.
 */
export default function IdePage() {
  const wsRef = useRef<WebSocket | null>(null);
  const [connectionState, setConnectionState] = useState<
    "disconnected" | "connecting" | "connected" | "error"
  >("disconnected");
  const [connectionError, setConnectionError] = useState<string | null>(null);

  // ADR-010's real enforcement point is the companion's own WebSocket
  // handler refusing the upgrade without consent — this state only
  // controls what the human sees; it never has to be perfectly trusted
  // client-side state, since a page that skipped it couldn't get a
  // working connection anyway.
  const [consentState, setConsentState] = useState<ConsentState>("checking");

  const [folderPath, setFolderPath] = useState<string | null>(null);
  const [folderInput, setFolderInput] = useState("");

  const [dirListings, setDirListings] = useState<Record<string, DirEntry[]>>(
    {},
  );
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  // Multi-file tabs: openTabs is the ordered, deduplicated list of paths
  // a human has opened this session; openFiles holds each one's own
  // fetched/edited state independently, so switching tabs is instant for
  // anything already loaded, and closing one tab never touches another.
  const [openTabs, setOpenTabs] = useState<string[]>([]);
  const [openFiles, setOpenFiles] = useState<Record<string, OpenFile>>({});
  const [activePath, setActivePath] = useState<string | null>(null);

  const [bottomTab, setBottomTab] = useState<BottomTab>("terminal");
  const terminalContainerRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<import("@xterm/xterm").Terminal | null>(null);
  const fitAddonRef = useRef<import("@xterm/addon-fit").FitAddon | null>(
    null,
  );
  const [shellRunning, setShellRunning] = useState(false);

  const send = (msg: Record<string, unknown>) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(msg));
    }
  };

  const connect = () => {
    if (!COMPANION_URL) return;
    setConnectionState("connecting");
    setConnectionError(null);
    const ws = new WebSocket(COMPANION_URL);
    wsRef.current = ws;

    ws.onopen = () => setConnectionState("connected");
    ws.onerror = () => setConnectionState("error");
    ws.onclose = () => setConnectionState("disconnected");
    ws.onmessage = (event) => {
      const msg = JSON.parse(event.data as string) as ServerMessage;
      handleServerMessage(msg);
    };
  };

  const handleServerMessage = (msg: ServerMessage) => {
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
          return {
            ...prev,
            [msg.path as string]: { ...existing, dirty: false },
          };
        });
        break;
      case "shell_started":
        setShellRunning(true);
        break;
      case "shell_output":
        terminalRef.current?.write(msg.data ?? "");
        break;
      case "shell_exited":
        setShellRunning(false);
        terminalRef.current?.write(
          `\r\n[process exited with code ${msg.exit_code}]\r\n`,
        );
        break;
      case "error":
        setConnectionError(msg.message ?? "Unknown companion error.");
        break;
    }
  };

  // Check real consent status before ever touching the WebSocket — the
  // companion's own handler enforces this regardless, but the human
  // should see the real dedicated consent screen instead of a raw
  // connection-refused error the first time they ever open this page.
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
    // Connect-on-mount, same justification as every other
    // fetch/connect-on-mount effect in this codebase for this lint rule.
    // Gated on real, confirmed consent — never fires while the consent
    // screen below is still what the human is looking at.
    if (consentState !== "granted") return;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    connect();
    return () => wsRef.current?.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [consentState]);

  // xterm.js is a vanilla-JS library, not a React component — mounted
  // imperatively into terminalContainerRef once, same pattern this
  // codebase would use for any non-React widget. Always mounted
  // (visibility toggled via CSS below, not conditional rendering) so
  // switching bottom-panel tabs never destroys the live shell session —
  // a real, running process shouldn't get killed just because a human
  // glanced at the Problems tab.
  useEffect(() => {
    if (!terminalContainerRef.current || terminalRef.current) return;
    let disposed = false;
    void (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);
      if (disposed || !terminalContainerRef.current) return;
      const term = new Terminal({
        convertEol: true,
        fontSize: 13,
        fontFamily: "var(--login-font-mono)",
        theme: { background: "#0d1117" },
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(terminalContainerRef.current);
      fit.fit();
      term.onData((data) => send({ type: "shell_input", data }));
      terminalRef.current = term;
      fitAddonRef.current = fit;
      const onResize = () => fit.fit();
      window.addEventListener("resize", onResize);
    })();
    return () => {
      disposed = true;
    };
  }, []);

  // Re-fit whenever the terminal tab becomes visible again — xterm
  // can't size itself correctly against a container that was
  // display:none a moment ago.
  useEffect(() => {
    if (bottomTab === "terminal") fitAddonRef.current?.fit();
  }, [bottomTab]);

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

  const openFile = (path: string) => {
    setActivePath(path);
    setOpenTabs((prev) => (prev.includes(path) ? prev : [...prev, path]));
    if (openFiles[path]) return;
    setOpenFiles((prev) => ({
      ...prev,
      [path]: { path, content: "", dirty: false, loading: true },
    }));
    send({ type: "read_file", path });
  };

  const closeTab = (path: string, e?: React.MouseEvent) => {
    e?.stopPropagation();
    setOpenTabs((prev) => {
      const idx = prev.indexOf(path);
      const next = prev.filter((p) => p !== path);
      if (activePath === path) {
        // Same "closing a tab focuses its neighbor" convention every
        // real tabbed editor uses, not just dropping focus entirely.
        setActivePath(next[idx] ?? next[idx - 1] ?? null);
      }
      return next;
    });
  };

  const saveActiveFile = () => {
    if (!activePath) return;
    const file = openFiles[activePath];
    if (!file) return;
    send({
      type: "write_file",
      path: activePath,
      content_base64: textToBase64(file.content),
    });
  };

  const renderEntries = (dirPath: string, depth: number) => {
    const entries = dirListings[dirPath];
    if (!entries) return null;
    return entries.map((entry) => {
      if (entry.is_dir) {
        const isOpen = expanded.has(entry.path);
        return (
          <div key={entry.path}>
            <button
              type="button"
              onClick={() => toggleDir(entry.path)}
              style={{ paddingLeft: `${depth * 14 + 4}px` }}
              className="flex w-full items-center gap-1 rounded py-1 pr-2 text-left text-[13px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
            >
              <span
                className={`flex shrink-0 transition-transform ${isOpen ? "rotate-90" : ""}`}
              >
                <ChevronRightIcon />
              </span>
              <FolderIcon />
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
          className={`flex w-full items-center gap-1.5 rounded py-1 pr-2 text-left text-[13px] ${
            activePath === entry.path
              ? "bg-[var(--login-surface-2)] text-[var(--login-text)]"
              : "text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
          }`}
        >
          <FileIcon />
          <span className="truncate">{entry.name}</span>
        </button>
      );
    });
  };

  if (!COMPANION_URL) {
    return (
      <main className="flex h-full items-center justify-center px-6 text-center">
        <p className="max-w-[420px] text-[13px] text-[var(--login-text-muted)]">
          NEXT_PUBLIC_COMPANION_WS_URL isn&apos;t set — see
          apps/web/.env.example.
        </p>
      </main>
    );
  }

  // ADR-010's consent screen — a real, dedicated, hard-to-miss gate, not
  // a checkbox in Settings. Nothing below this point (file tree, editor,
  // terminal) renders until a human has actually seen this and clicked
  // through it; "checking" and "unreachable" also block, so this can
  // never flash the real UI before consent is confirmed one way or the
  // other. The companion's own WebSocket handler enforces this
  // independently — this screen is what makes the first real use of it
  // an informed decision, not just the reason the connection happens to
  // work.
  if (consentState !== "granted") {
    if (consentState === "checking") {
      return (
        <main className="flex h-full items-center justify-center px-6 text-center">
          <p className="text-[13px] text-[var(--login-text-muted)]">
            Checking companion status…
          </p>
        </main>
      );
    }
    if (consentState === "unreachable") {
      return (
        <main className="flex h-full items-center justify-center px-6 text-center">
          <div className="flex max-w-[420px] flex-col gap-3">
            <p className="text-[13px] text-[var(--login-text-muted)]">
              Couldn&apos;t reach the local companion process at{" "}
              {companionHttpBase(COMPANION_URL)} — start it and try again.
            </p>
            <button
              type="button"
              onClick={() => setConsentState("checking")}
              className="mx-auto rounded-lg border border-[var(--login-border-strong)] px-4 py-2 text-[13px] text-[var(--login-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--login-text)]"
            >
              Try again
            </button>
          </div>
        </main>
      );
    }
    return (
      <main className="flex h-full items-center justify-center px-6 py-10">
        <div className="flex w-full max-w-[560px] flex-col gap-5 rounded-xl border border-[var(--room-warn)] bg-[var(--login-surface-2)] p-6">
          <h1 className="text-[17px] font-semibold text-[var(--login-text)]">
            Before you connect: this enables real command execution
          </h1>
          <p className="text-[13.5px] leading-relaxed text-[var(--login-text-secondary)]">
            The Harmonia IDE talks to a small local process (the
            companion) running on this machine. Once connected, it can
            read and write real files and run a real shell — on your own
            request, or an agent&apos;s — in the folder you open. This is
            fundamentally different from anything else in Harmonia: every
            other action here has a bounded, well-understood effect; a
            real shell does not.
          </p>
          <p className="text-[13.5px] leading-relaxed text-[var(--login-text-secondary)]">
            An agent can only act while you are actively watching this
            session — the moment you leave, its file and command tools
            become unavailable. Every action an agent takes is marked
            visibly in the terminal, distinct from anything you type
            yourself.
          </p>
          <p className="text-[13.5px] leading-relaxed text-[var(--login-text-secondary)]">
            There is no command blocklist — a real shell makes one
            trivially easy to bypass, and a blocklist creates false
            confidence worse than having none. The real safety net is
            git: damage to a git-tracked file is recoverable through its
            real history. Damage outside a git repository&apos;s scope,
            or to files git isn&apos;t tracking, is not.
          </p>
          <p className="text-[13.5px] leading-relaxed text-[var(--login-text-secondary)]">
            This consent is remembered on this machine, for this
            companion installation, until you revoke it yourself.
          </p>
          <button
            type="button"
            disabled={consentState === "granting"}
            onClick={() => void grantConsent()}
            className="self-start rounded-lg bg-[var(--login-accent)] px-4 py-2 text-[13.5px] font-medium text-[var(--login-bg)] hover:bg-[#63e0d1] disabled:cursor-not-allowed disabled:opacity-60"
          >
            {consentState === "granting"
              ? "Enabling…"
              : "I understand — enable the companion"}
          </button>
        </div>
      </main>
    );
  }

  const activeFile = activePath ? openFiles[activePath] : null;

  return (
    <main className="flex h-full min-w-0 flex-col">
      {!folderPath ? (
        <div className="flex flex-1 items-center justify-center">
          <div className="flex w-full max-w-[440px] flex-col gap-3 px-6 text-center">
            <h2 className="text-[15px] font-medium text-[var(--login-text)]">
              Open a project
            </h2>
            <p className="text-[13px] text-[var(--login-text-muted)]">
              Enter the real absolute path of a folder on this machine —
              the companion process reads and writes it directly.
            </p>
            <input
              value={folderInput}
              onChange={(e) => setFolderInput(e.target.value)}
              placeholder="C:\Users\you\projects\my-app"
              className="rounded-lg border border-[var(--login-border-strong)] bg-transparent px-3 py-2 text-[13px] text-[var(--login-text)] outline-none focus:border-[var(--login-accent)]"
            />
            <button
              type="button"
              disabled={connectionState !== "connected" || !folderInput}
              onClick={() => send({ type: "open_folder", path: folderInput })}
              className="rounded-lg bg-[var(--login-accent)] px-4 py-2 text-[13.5px] font-medium text-[var(--login-bg)] hover:bg-[#63e0d1] disabled:cursor-not-allowed disabled:opacity-40"
            >
              Open folder
            </button>
            {(connectionError || connectionState === "error") && (
              <p className="text-[13px] text-[var(--room-warn)]">
                {connectionError ??
                  "Couldn't reach the companion process — is it running?"}
              </p>
            )}
          </div>
        </div>
      ) : (
        <div className="flex min-h-0 flex-1">
          {/* Left: the file explorer only — no VS Code icon rail
              (source control, extensions, remote) alongside it, since
              none of that reflects anything real in this stage. */}
          <div className="flex w-[240px] shrink-0 flex-col overflow-y-auto border-r border-[var(--login-border)] p-2">
            <div className="mb-2 px-1.5 font-[family-name:var(--login-font-mono)] text-[11px] uppercase tracking-wide text-[var(--login-text-muted)]">
              Explorer
            </div>
            {renderEntries("", 0)}
          </div>

          <div className="flex min-w-0 flex-1 flex-col">
            <div className="flex min-h-0 flex-[2] flex-col">
              {/* Tab bar — a real second tab on a second file, never
                  replacing the first. */}
              {openTabs.length > 0 && (
                <div className="no-scrollbar flex shrink-0 items-stretch overflow-x-auto border-b border-[var(--login-border)]">
                  {openTabs.map((path) => {
                    const f = openFiles[path];
                    return (
                      <button
                        key={path}
                        type="button"
                        onClick={() => setActivePath(path)}
                        className={`group flex shrink-0 items-center gap-2 border-r border-[var(--login-border)] px-3 py-2 text-[12.5px] ${
                          activePath === path
                            ? "bg-[var(--login-surface-2)] text-[var(--login-text)]"
                            : "text-[var(--login-text-muted)] hover:text-[var(--login-text-secondary)]"
                        }`}
                      >
                        <FileIcon />
                        <span className="max-w-[160px] truncate">
                          {path.split("/").pop()}
                          {f?.dirty ? " ●" : ""}
                        </span>
                        <span
                          role="button"
                          tabIndex={-1}
                          onClick={(e) => closeTab(path, e)}
                          className="rounded text-[var(--login-text-muted)] opacity-0 hover:bg-[var(--login-border-strong)] hover:text-[var(--login-text)] group-hover:opacity-100"
                        >
                          <CloseIcon />
                        </span>
                      </button>
                    );
                  })}
                  {activePath && (
                    <button
                      type="button"
                      disabled={!activeFile?.dirty}
                      onClick={saveActiveFile}
                      className="ml-auto shrink-0 self-center rounded-md border border-[var(--login-border-strong)] px-2.5 py-1 text-[12px] text-[var(--login-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--login-text)] disabled:cursor-not-allowed disabled:opacity-40"
                    >
                      Save
                    </button>
                  )}
                </div>
              )}
              <div className="min-h-0 flex-1">
                {!activePath && (
                  <div className="flex h-full items-center justify-center text-[13px] text-[var(--login-text-muted)]">
                    Select a file to edit it.
                  </div>
                )}
                {activeFile && !activeFile.loading && (
                  <MonacoEditor
                    path={activeFile.path}
                    language={languageForPath(activeFile.path)}
                    value={activeFile.content}
                    theme="vs-dark"
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
                    options={{ minimap: { enabled: false }, fontSize: 13 }}
                  />
                )}
              </div>
            </div>

            {/* Bottom panel — Problems/Output/Terminal, VS Code's own
                structure. Terminal is the one real, working shell in
                this app — see the standalone-IDE-only companion
                process; there is no fake or stubbed terminal here. */}
            <div className="flex h-[220px] shrink-0 flex-col border-t border-[var(--login-border)]">
              <div className="flex shrink-0 items-center justify-between px-2 pt-1.5">
                <div className="flex items-center gap-1">
                  {(["problems", "output", "terminal"] as const).map(
                    (tab) => (
                      <button
                        key={tab}
                        type="button"
                        onClick={() => setBottomTab(tab)}
                        className={`rounded-t-md px-2.5 py-1.5 text-[12px] capitalize ${
                          bottomTab === tab
                            ? "bg-[var(--login-surface-2)] text-[var(--login-text)]"
                            : "text-[var(--login-text-muted)] hover:text-[var(--login-text-secondary)]"
                        }`}
                      >
                        {tab}
                      </button>
                    ),
                  )}
                </div>
                {bottomTab === "terminal" && !shellRunning && (
                  <button
                    type="button"
                    onClick={() => send({ type: "start_shell" })}
                    className="mr-1 rounded-md border border-[var(--login-border-strong)] px-2.5 py-1 text-[12px] text-[var(--login-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--login-text)]"
                  >
                    Start shell
                  </button>
                )}
              </div>
              <div className="min-h-0 flex-1 bg-[var(--login-surface-2)]/40">
                <div
                  hidden={bottomTab !== "problems"}
                  className="h-full overflow-y-auto px-4 py-3"
                >
                  <p className="text-[12.5px] text-[var(--login-text-muted)]">
                    Nothing to show yet.
                  </p>
                </div>
                <div
                  hidden={bottomTab !== "output"}
                  className="h-full overflow-y-auto px-4 py-3"
                >
                  <p className="text-[12.5px] text-[var(--login-text-muted)]">
                    Nothing to show yet.
                  </p>
                </div>
                {/* Always mounted, never unmounted on tab switch — see
                    the xterm-mount effect's own comment for why. */}
                <div
                  hidden={bottomTab !== "terminal"}
                  className="h-full px-2 py-1"
                >
                  <div ref={terminalContainerRef} className="h-full" />
                </div>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Status bar — real data: companion connection state and the
          real open folder path. */}
      <div className="flex shrink-0 items-center gap-4 border-t border-[var(--login-border)] bg-[var(--login-surface-2)] px-3 py-1 font-[family-name:var(--login-font-mono)] text-[11px] text-[var(--login-text-muted)]">
        <span>
          companion:{" "}
          {connectionState === "connected"
            ? "● connected"
            : connectionState === "connecting"
              ? "○ connecting…"
              : connectionState === "error"
                ? "○ connection failed"
                : "○ disconnected"}
        </span>
        {folderPath && <span className="truncate">{folderPath}</span>}
        {connectionError && (
          <span className="ml-auto text-[var(--room-warn)]">
            {connectionError}
          </span>
        )}
      </div>
    </main>
  );
}
