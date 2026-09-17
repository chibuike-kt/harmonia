"use client";

import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";
import { OrbMark } from "@/components/OrbMark";
import { PROVIDER_TAG_COLORS, DEFAULT_TAG_COLOR } from "@/components/providerLogos";
import { Terminal, type TerminalHandle } from "@/components/ide/Terminal";

export type TranscriptEntry =
  | {
      kind: "chat";
      id: string;
      sender: "human" | "agent";
      agentName?: string;
      provider?: string;
      content: string;
    }
  | { kind: "error"; id: string; text: string };

export type BottomTab = "problems" | "output" | "terminal";
export type TerminalMode = "shell" | "chat";

interface TermServerMessage {
  type: string;
  terminal_id?: string;
  data?: string;
  cwd?: string;
  exit_code?: number;
  message?: string;
}

interface TermState {
  id: string;
  cwd: string;
  exited: boolean;
  exitCode: number | null;
}

// Which real terminal a create_terminal request was for — the
// terminal_created response that answers it doesn't echo this back, so
// this session's own request needs to remember which pane (or split-open
// intent) asked, the same way pendingRelayAction (page.tsx) remembers
// which relay action a companion response answers.
type PaneKey = "main" | "split";

function AgentTag({ name, provider }: { name: string; provider?: string }) {
  const color = provider
    ? (PROVIDER_TAG_COLORS[provider] ?? DEFAULT_TAG_COLOR)
    : DEFAULT_TAG_COLOR;
  return (
    <span
      className="flex h-[18px] shrink-0 items-center gap-1 rounded-[5px] border px-1.5 text-[10px]"
      style={{ background: color.bg, color: color.fg, borderColor: color.border }}
    >
      <OrbMark size={9} />
      {name}
    </span>
  );
}

function HumanTag() {
  return (
    <span className="flex h-[18px] shrink-0 items-center rounded-[5px] border border-[var(--ide-border-strong)] bg-[var(--ide-surface-2)] px-1.5 text-[10px] font-semibold text-[var(--ide-text-secondary)]">
      you
    </span>
  );
}

function cwdLabel(cwd: string): string {
  if (!cwd) return "shell";
  const parts = cwd.split(/[/\\]/).filter(Boolean);
  return parts[parts.length - 1] ?? cwd;
}

function ChevronDown() {
  return (
    <svg width="10" height="10" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8">
      <path d="M4 6l4 4 4-4" />
    </svg>
  );
}

function ToolbarIconButton({
  title,
  onClick,
  disabled,
  children,
}: {
  title: string;
  onClick: () => void;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      disabled={disabled}
      className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-[var(--ide-text-muted)] hover:bg-[var(--ide-surface-2)] hover:text-[var(--ide-text)] disabled:opacity-30 disabled:hover:bg-transparent"
    >
      {children}
    </button>
  );
}

// TerminalSelector: the real dropdown/selector this feature's own
// requirement names explicitly ("matching VS Code's own terminal-picker
// convention") — shows the pane's active terminal's live cwd label,
// opens a list of every real terminal this session has open to switch
// which one this pane displays.
function TerminalSelector({
  terminals,
  activeId,
  onSelect,
}: {
  terminals: TermState[];
  activeId: string | null;
  onSelect: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const active = terminals.find((t) => t.id === activeId);
  const index = terminals.findIndex((t) => t.id === activeId);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);

  return (
    <div className="relative" onMouseDown={(e) => e.stopPropagation()}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11.5px] text-[var(--ide-text-secondary)] hover:bg-[var(--ide-surface-2)] hover:text-[var(--ide-text)]"
      >
        <span
          className={`h-[7px] w-[7px] shrink-0 rounded-full ${
            active?.exited ? "bg-[var(--ide-text-muted)]" : "bg-[var(--login-accent)]"
          }`}
        />
        <span className="max-w-[140px] truncate font-[family-name:var(--login-font-mono)]">
          {index >= 0 ? `${index + 1}: ` : ""}
          {active ? cwdLabel(active.cwd) : "no terminal"}
          {active?.exited ? " (exited)" : ""}
        </span>
        <ChevronDown />
      </button>
      {open && (
        <div className="absolute top-full left-0 z-20 mt-1 min-w-[220px] rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface-2)] p-1 shadow-xl">
          {terminals.length === 0 && (
            <div className="px-2 py-1.5 text-[12px] text-[var(--ide-text-muted)]">
              No terminals open
            </div>
          )}
          {terminals.map((t, i) => (
            <button
              key={t.id}
              type="button"
              onClick={() => {
                onSelect(t.id);
                setOpen(false);
              }}
              className={`flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[12.5px] ${
                t.id === activeId
                  ? "bg-[var(--ide-surface)] text-[var(--ide-text)]"
                  : "text-[var(--ide-text-secondary)] hover:bg-[var(--ide-surface)]"
              }`}
            >
              <span
                className={`h-[7px] w-[7px] shrink-0 rounded-full ${
                  t.exited ? "bg-[var(--ide-text-muted)]" : "bg-[var(--login-accent)]"
                }`}
              />
              <span className="flex-1 truncate font-[family-name:var(--login-font-mono)]">
                {i + 1}: {cwdLabel(t.cwd)}
              </span>
              {t.exited && <span className="text-[10px] text-[var(--ide-text-muted)]">exited</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export interface TerminalPanelHandle {
  dispatch: (msg: TermServerMessage) => void;
  createTerminal: () => void;
  reset: () => void;
}

/**
 * The bottom panel's real content — Problems/Output/Terminal tabs. The
 * Terminal tab is either a genuine multi-instance xterm.js terminal
 * system (mode "shell": real PTY sessions via the companion, a real
 * VS Code-style selector, an optional real split pane, each terminal's
 * own real scrollback/search/copy-paste/resize/kill/restart) or this
 * session's paired-room chat (mode "chat") — a deliberate, explicit
 * toggle (see this component's own git history) rather than guessing
 * intent from typed text.
 *
 * This component owns every real terminal instance's lifecycle (the
 * companion protocol only speaks in terminal ids, not pane assignments)
 * — the parent page only owns the WebSocket itself, handing every
 * terminal_* server message to dispatch() and getting `send` calls back
 * for every real client message this panel needs to issue.
 */
export const TerminalPanel = forwardRef<
  TerminalPanelHandle,
  {
    bottomTab: BottomTab;
    onBottomTabChange: (tab: BottomTab) => void;
    onClose: () => void;
    transcript: TranscriptEntry[];
    mode: TerminalMode;
    onModeChange: (mode: TerminalMode) => void;
    chatValue: string;
    onChatChange: (value: string) => void;
    onChatSubmit: () => void;
    send: (msg: Record<string, unknown>) => void;
    folderOpen: boolean;
  }
>(function TerminalPanel(
  {
    bottomTab,
    onBottomTabChange,
    onClose,
    transcript,
    mode,
    onModeChange,
    chatValue,
    onChatChange,
    onChatSubmit,
    send,
    folderOpen,
  },
  ref,
) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const chatInputRef = useRef<HTMLInputElement>(null);

  const [terminals, setTerminals] = useState<TermState[]>([]);
  const [mainId, setMainId] = useState<string | null>(null);
  const [splitId, setSplitId] = useState<string | null>(null);
  const handlesRef = useRef<Map<string, TerminalHandle>>(new Map());
  const pendingCreateRef = useRef<PaneKey>("main");

  const requestNewTerminal = useCallback(
    (pane: PaneKey) => {
      pendingCreateRef.current = pane;
      send({ type: "create_terminal" });
    },
    [send],
  );

  const dispatch = useCallback(
    (msg: TermServerMessage) => {
      switch (msg.type) {
        case "terminal_created": {
          const id = msg.terminal_id;
          if (!id) break;
          setTerminals((prev) => [...prev, { id, cwd: msg.cwd ?? "", exited: false, exitCode: null }]);
          if (pendingCreateRef.current === "split") setSplitId(id);
          else setMainId(id);
          break;
        }
        case "terminal_output": {
          const id = msg.terminal_id;
          if (!id) break;
          handlesRef.current.get(id)?.write(msg.data ?? "");
          break;
        }
        case "terminal_cwd": {
          const id = msg.terminal_id;
          if (!id) break;
          setTerminals((prev) => prev.map((t) => (t.id === id ? { ...t, cwd: msg.cwd ?? t.cwd } : t)));
          break;
        }
        case "terminal_exited": {
          const id = msg.terminal_id;
          if (!id) break;
          setTerminals((prev) =>
            prev.map((t) => (t.id === id ? { ...t, exited: true, exitCode: msg.exit_code ?? null } : t)),
          );
          handlesRef.current
            .get(id)
            ?.write(`\r\n\x1b[2m[process exited with code ${msg.exit_code ?? 0}]\x1b[0m\r\n`);
          break;
        }
      }
    },
    [],
  );

  const reset = useCallback(() => {
    setTerminals([]);
    setMainId(null);
    setSplitId(null);
    handlesRef.current.clear();
  }, []);

  useImperativeHandle(
    ref,
    () => ({
      dispatch,
      createTerminal: () => requestNewTerminal("main"),
      reset,
    }),
    [dispatch, requestNewTerminal, reset],
  );

  useEffect(() => {
    bodyRef.current?.scrollTo({ top: bodyRef.current.scrollHeight });
  }, [transcript]);

  const killTerminal = useCallback(
    (id: string) => {
      send({ type: "kill_terminal", terminal_id: id });
    },
    [send],
  );

  const closeTerminal = useCallback(
    (id: string) => {
      const t = terminals.find((x) => x.id === id);
      if (t && !t.exited) send({ type: "kill_terminal", terminal_id: id });
      const remaining = terminals.filter((x) => x.id !== id);
      setTerminals(remaining);
      handlesRef.current.delete(id);
      setMainId((cur) => (cur === id ? (remaining[0]?.id ?? null) : cur));
      setSplitId((cur) => (cur === id ? null : cur));
    },
    [send, terminals],
  );

  const restartTerminal = useCallback(
    (id: string, pane: PaneKey) => {
      closeTerminal(id);
      requestNewTerminal(pane);
    },
    [closeTerminal, requestNewTerminal],
  );

  const toggleSplit = useCallback(() => {
    if (splitId) {
      setSplitId(null);
    } else {
      requestNewTerminal("split");
    }
  }, [splitId, requestNewTerminal]);

  return (
    <div className="flex h-full flex-col">
      <div className="flex h-[34px] shrink-0 items-center justify-between px-3">
        <div className="flex h-full items-center gap-4">
          {(["problems", "output", "terminal"] as const).map((tab) => (
            <button
              key={tab}
              type="button"
              onClick={() => onBottomTabChange(tab)}
              className={`h-full border-b-2 text-[11.5px] capitalize transition-colors ${
                bottomTab === tab
                  ? "border-[var(--login-accent)] text-[var(--ide-text)]"
                  : "border-transparent text-[var(--ide-text-muted)] hover:text-[var(--ide-text-secondary)]"
              }`}
            >
              {tab}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-0.5">
          {bottomTab === "terminal" && mode === "shell" && (
            <>
              <ToolbarIconButton title="New terminal" onClick={() => requestNewTerminal("main")}>
                <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6">
                  <path d="M8 2v12M2 8h12" />
                </svg>
              </ToolbarIconButton>
              <ToolbarIconButton
                title={splitId ? "Close split" : "Split terminal"}
                onClick={toggleSplit}
                disabled={!mainId}
              >
                <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6">
                  <rect x="2" y="3" width="12" height="10" rx="1.5" />
                  <path d="M8 3v10" />
                </svg>
              </ToolbarIconButton>
              <ToolbarIconButton
                title="Kill terminal"
                onClick={() => mainId && killTerminal(mainId)}
                disabled={!mainId}
              >
                <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6">
                  <path d="M3 4h10M6.5 4V2.5h3V4M4.5 4l.6 9a1 1 0 001 .9h3.8a1 1 0 001-.9l.6-9" />
                </svg>
              </ToolbarIconButton>
            </>
          )}
          <ToolbarIconButton title="Close panel (Ctrl+`)" onClick={onClose}>
            <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </ToolbarIconButton>
        </div>
      </div>

      <div className="min-h-0 flex-1 bg-[rgba(26,31,41,.4)]">
        <div hidden={bottomTab !== "problems"} className="h-full overflow-y-auto px-4 py-3">
          <p className="text-[12.5px] text-[var(--ide-text-muted)]">Nothing to show yet.</p>
        </div>
        <div hidden={bottomTab !== "output"} className="h-full overflow-y-auto px-4 py-3">
          <p className="text-[12.5px] text-[var(--ide-text-muted)]">Nothing to show yet.</p>
        </div>

        <div hidden={bottomTab !== "terminal"} className="flex h-full flex-col">
          <div className="flex shrink-0 items-center gap-1 border-b border-[var(--ide-border)] px-2">
            <div className="flex rounded-md p-0.5">
              {(["shell", "chat"] as const).map((m) => (
                <button
                  key={m}
                  type="button"
                  onClick={() => onModeChange(m)}
                  className={`rounded px-2 py-1 text-[11px] capitalize ${
                    mode === m
                      ? "bg-[var(--ide-surface-2)] text-[var(--ide-text)]"
                      : "text-[var(--ide-text-muted)]"
                  }`}
                >
                  {m}
                </button>
              ))}
            </div>
          </div>

          {mode === "shell" ? (
            !folderOpen ? (
              <div className="flex flex-1 items-center justify-center">
                <p className="text-[12.5px] text-[var(--ide-text-muted)]">
                  Open a folder to start a real terminal.
                </p>
              </div>
            ) : terminals.length === 0 ? (
              <div className="flex flex-1 flex-col items-center justify-center gap-2">
                <p className="text-[12.5px] text-[var(--ide-text-muted)]">No terminal running.</p>
                <button
                  type="button"
                  onClick={() => requestNewTerminal("main")}
                  className="rounded-md border border-[var(--ide-border-strong)] px-2.5 py-1 text-[11.5px] text-[var(--ide-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--ide-text)]"
                >
                  New Terminal
                </button>
              </div>
            ) : (
              <div className="flex min-h-0 flex-1">
                {terminals.map((t) => {
                  const visiblePane: PaneKey | null =
                    t.id === mainId ? "main" : t.id === splitId ? "split" : null;
                  return (
                    <div
                      key={t.id}
                      hidden={visiblePane === null}
                      className="flex min-h-0 min-w-0 flex-1 flex-col border-r border-[var(--ide-border)] last:border-r-0"
                    >
                      <div className="flex h-7 shrink-0 items-center justify-between gap-1 border-b border-[var(--ide-border)] pl-1.5 pr-1">
                        <TerminalSelector
                          terminals={terminals}
                          activeId={t.id}
                          onSelect={(id) => {
                            if (visiblePane === "split") setSplitId(id);
                            else setMainId(id);
                          }}
                        />
                        <div className="flex items-center gap-0.5">
                          <ToolbarIconButton
                            title="Restart terminal"
                            onClick={() => visiblePane && restartTerminal(t.id, visiblePane)}
                          >
                            <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6">
                              <path d="M13 8a5 5 0 11-1.6-3.65M13 3v3.5H9.5" />
                            </svg>
                          </ToolbarIconButton>
                          <ToolbarIconButton title="Kill" onClick={() => killTerminal(t.id)} disabled={t.exited}>
                            <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.7">
                              <path d="M4 4l8 8M12 4l-8 8" />
                            </svg>
                          </ToolbarIconButton>
                          {visiblePane === "split" && (
                            <ToolbarIconButton title="Close split" onClick={() => setSplitId(null)}>
                              <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.7">
                                <path d="M4 4l8 8M12 4l-8 8" />
                              </svg>
                            </ToolbarIconButton>
                          )}
                        </div>
                      </div>
                      <Terminal
                        ref={(handle) => {
                          if (handle) handlesRef.current.set(t.id, handle);
                          else handlesRef.current.delete(t.id);
                        }}
                        onData={(data) => send({ type: "terminal_input", terminal_id: t.id, data })}
                        onResize={(cols, rows) =>
                          send({ type: "terminal_resize", terminal_id: t.id, cols, rows })
                        }
                      />
                    </div>
                  );
                })}
              </div>
            )
          ) : (
            <>
              <div
                ref={bodyRef}
                className="no-scrollbar min-h-0 flex-1 overflow-y-auto px-3.5 py-2.5 font-[family-name:var(--login-font-mono)] text-[12.5px]"
              >
                {transcript.length === 0 && (
                  <p className="text-[var(--ide-text-muted)]">No activity yet — send a message below.</p>
                )}
                {transcript.map((entry) =>
                  entry.kind === "chat" ? (
                    <div key={entry.id} className="mb-[5px] flex items-start gap-2">
                      {entry.sender === "human" ? (
                        <HumanTag />
                      ) : (
                        <AgentTag name={entry.agentName ?? "agent"} provider={entry.provider} />
                      )}
                      <span
                        className={
                          entry.sender === "human"
                            ? "text-[var(--ide-text)]"
                            : "leading-relaxed text-[var(--ide-text-secondary)]"
                        }
                      >
                        {entry.content}
                      </span>
                    </div>
                  ) : (
                    <div key={entry.id} className="mb-[5px] pl-5 whitespace-pre-wrap text-[var(--room-warn)]">
                      {entry.text}
                    </div>
                  ),
                )}
              </div>
              <div className="flex shrink-0 items-center gap-2 border-t border-[var(--ide-border)] px-3 py-2">
                <input
                  ref={chatInputRef}
                  value={chatValue}
                  onChange={(e) => onChatChange(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key !== "Enter") return;
                    e.preventDefault();
                    onChatSubmit();
                  }}
                  placeholder="Message the room — @Name to address a specific agent…"
                  className="flex-1 bg-transparent font-[family-name:var(--login-font-mono)] text-[12.5px] text-[var(--ide-text)] outline-none placeholder:text-[var(--ide-text-muted)]"
                />
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
});
