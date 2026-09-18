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
import { Tooltip } from "@/components/Tooltip";
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
      /** True only for a human message sent into a 2+-agent room that
       *  addressed no one — mirrors MessageRow.tsx's own real "Addressed
       *  no one" convention from the main Rooms page (same reasoning:
       *  correct, intentional silence per ADR-004/ADR-007, but silence
       *  alone reads as broken rather than as a rule if nothing here
       *  ever says so). */
      unaddressed?: boolean;
    }
  | { kind: "error"; id: string; text: string };

export type BottomTab = "problems" | "output" | "terminal";
export type TerminalMode = "shell" | "chat" | "agent";

// AgentAvailable is the minimal shape the agent-session start form needs
// from this room's real roster — id/name/provider only, not the full
// RoomAgentDTO the parent page keeps for other purposes.
export interface AgentAvailable {
  id: string;
  name: string;
  provider: string;
}

// AgentSessionState mirrors internal/agentloop's own real wire shape
// (both its POST .../agent_loop/start response and its live
// agent_loop_status SSE payload) — the parent page owns fetching/updating
// this; this component only ever renders it.
export interface AgentSessionState {
  sessionId: string;
  task: string;
  cycle: number;
  maxCycles: number;
  spendUsd: number;
  dollarCapUsd: number;
  state: string;
  message?: string;
}

export interface AgentStartForm {
  agentId: string;
  task: string;
  maxCycles: number;
  dollarCapUsd: number;
  wallClockSeconds: number;
}

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
    <Tooltip label={title} side="top" align="end">
      <button
        type="button"
        onClick={onClick}
        disabled={disabled}
        className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-[var(--ide-text-muted)] hover:bg-[var(--ide-surface-2)] hover:text-[var(--ide-text)] disabled:opacity-30 disabled:hover:bg-transparent"
      >
        {children}
      </button>
    </Tooltip>
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
  // adoptAgentTerminal registers a terminal the backend created for a
  // sustained agent-loop session (ADR-011) as this panel's own agent
  // terminal — deliberately never assigned as the shell mode's main/split
  // pane, so a running session's own terminal can never hijack whatever
  // real shell a human already has open.
  adoptAgentTerminal: (id: string) => void;
}

const AGENT_STATE_LABEL: Record<string, string> = {
  running: "Running",
  completed: "Completed",
  stopped_bound: "Stopped",
  stopped_presence: "Stopped — presence lost",
  stopped_manual: "Stopped by you",
  failed: "Failed",
};

function formatUSD(n: number): string {
  if (n === 0) return "$0.00";
  if (n < 0.01) return `$${n.toFixed(5)}`;
  return `$${n.toFixed(2)}`;
}

/** The real, always-visible status bar for a running (or just-finished)
 *  agent-loop session — cycle count against its real cap, real running
 *  spend against its real dollar cap, current state, and a real Stop
 *  control distinct from presence loss (ADR-011 batch A items 2/3). */
function AgentStatusBar({
  session,
  onStop,
}: {
  session: AgentSessionState;
  onStop: () => void;
}) {
  const running = session.state === "running";
  return (
    <div className="flex shrink-0 flex-col gap-1 border-b border-[var(--ide-border)] px-3 py-2 text-[11.5px]">
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-[var(--ide-text-secondary)]">{session.task}</span>
        {running && (
          <button
            type="button"
            onClick={onStop}
            className="shrink-0 rounded-md border border-[var(--room-warn)] px-2 py-0.5 text-[11px] font-semibold text-[var(--room-warn)] hover:bg-[var(--room-warn)] hover:text-[var(--ide-surface)]"
          >
            Stop
          </button>
        )}
      </div>
      <div className="flex items-center gap-3 font-[family-name:var(--login-font-mono)] text-[var(--ide-text-muted)]">
        <span>
          cycle {session.cycle}/{session.maxCycles}
        </span>
        <span>
          {formatUSD(session.spendUsd)} / {formatUSD(session.dollarCapUsd)}
        </span>
        <span className={running ? "text-[var(--login-accent)]" : "text-[var(--ide-text-secondary)]"}>
          {AGENT_STATE_LABEL[session.state] ?? session.state}
        </span>
      </div>
      {!running && session.message && (
        <div className="text-[var(--ide-text-secondary)]">{session.message}</div>
      )}
    </div>
  );
}

/** The real form ADR-011 requires before any sustained session starts —
 *  three real numbers (max cycles, dollar cap, wall-clock limit) plus a
 *  task and a target agent, none defaulted: Start is disabled until every
 *  field is genuinely filled in. */
function AgentStartFormPanel({
  agents,
  onStart,
  error,
  disabled,
}: {
  agents: AgentAvailable[];
  onStart: (form: AgentStartForm) => void;
  error: string | null;
  disabled: boolean;
}) {
  const [agentId, setAgentId] = useState(agents[0]?.id ?? "");
  const [task, setTask] = useState("");
  const [maxCycles, setMaxCycles] = useState("");
  const [dollarCap, setDollarCap] = useState("");
  const [wallClockMinutes, setWallClockMinutes] = useState("");

  const maxCyclesN = Number(maxCycles);
  const dollarCapN = Number(dollarCap);
  const wallClockN = Number(wallClockMinutes);
  const canStart =
    !disabled &&
    agentId !== "" &&
    task.trim() !== "" &&
    Number.isFinite(maxCyclesN) &&
    maxCyclesN > 0 &&
    Number.isFinite(dollarCapN) &&
    dollarCapN > 0 &&
    Number.isFinite(wallClockN) &&
    wallClockN > 0;

  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 overflow-y-auto px-6 py-6">
      <div className="w-full max-w-[360px] rounded-lg border border-[var(--ide-border-strong)] bg-[var(--ide-surface-2)] p-4">
        <p className="mb-3 text-[12.5px] font-semibold text-[var(--ide-text)]">Start a sustained agent session</p>
        {disabled ? (
          <p className="text-[12px] text-[var(--ide-text-muted)]">Open a folder to start a sustained agent session.</p>
        ) : agents.length === 0 ? (
          <p className="text-[12px] text-[var(--ide-text-muted)]">No agents in this room yet.</p>
        ) : (
          <div className="flex flex-col gap-2.5">
            <label className="flex flex-col gap-1 text-[11px] text-[var(--ide-text-muted)]">
              Agent
              <select
                value={agentId}
                onChange={(e) => setAgentId(e.target.value)}
                className="rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] px-2 py-1 text-[12px] text-[var(--ide-text)]"
              >
                {agents.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-[11px] text-[var(--ide-text-muted)]">
              Task
              <textarea
                value={task}
                onChange={(e) => setTask(e.target.value)}
                rows={3}
                placeholder="What should this session accomplish?"
                className="resize-none rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] px-2 py-1 text-[12px] text-[var(--ide-text)] placeholder:text-[var(--ide-text-muted)]"
              />
            </label>
            <div className="grid grid-cols-3 gap-2">
              <label className="flex flex-col gap-1 text-[11px] text-[var(--ide-text-muted)]">
                Max cycles
                <input
                  type="number"
                  min={1}
                  value={maxCycles}
                  onChange={(e) => setMaxCycles(e.target.value)}
                  placeholder="20"
                  className="rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] px-2 py-1 text-[12px] text-[var(--ide-text)] placeholder:text-[var(--ide-text-muted)]"
                />
              </label>
              <label className="flex flex-col gap-1 text-[11px] text-[var(--ide-text-muted)]">
                Cost cap ($)
                <input
                  type="number"
                  min={0.01}
                  step={0.01}
                  value={dollarCap}
                  onChange={(e) => setDollarCap(e.target.value)}
                  placeholder="2.00"
                  className="rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] px-2 py-1 text-[12px] text-[var(--ide-text)] placeholder:text-[var(--ide-text-muted)]"
                />
              </label>
              <label className="flex flex-col gap-1 text-[11px] text-[var(--ide-text-muted)]">
                Minutes
                <input
                  type="number"
                  min={1}
                  value={wallClockMinutes}
                  onChange={(e) => setWallClockMinutes(e.target.value)}
                  placeholder="15"
                  className="rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] px-2 py-1 text-[12px] text-[var(--ide-text)] placeholder:text-[var(--ide-text-muted)]"
                />
              </label>
            </div>
            {error && <p className="text-[11.5px] text-[var(--room-warn)]">{error}</p>}
            <button
              type="button"
              disabled={!canStart}
              onClick={() =>
                onStart({
                  agentId,
                  task: task.trim(),
                  maxCycles: Math.floor(maxCyclesN),
                  dollarCapUsd: dollarCapN,
                  wallClockSeconds: Math.floor(wallClockN * 60),
                })
              }
              className="mt-1 rounded-md bg-[var(--login-accent)] px-2.5 py-1.5 text-[12px] font-semibold text-[var(--ide-surface)] disabled:cursor-not-allowed disabled:opacity-40"
            >
              Start session
            </button>
          </div>
        )}
      </div>
    </div>
  );
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
    agentAgents: AgentAvailable[];
    agentSession: AgentSessionState | null;
    agentStartError: string | null;
    onStartAgentSession: (form: AgentStartForm) => void;
    onStopAgentSession: () => void;
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
    agentAgents,
    agentSession,
    agentStartError,
    onStartAgentSession,
    onStopAgentSession,
  },
  ref,
) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const chatInputRef = useRef<HTMLInputElement>(null);

  const [terminals, setTerminals] = useState<TermState[]>([]);
  const [mainId, setMainId] = useState<string | null>(null);
  const [splitId, setSplitId] = useState<string | null>(null);
  const [agentTerminalId, setAgentTerminalId] = useState<string | null>(null);
  const handlesRef = useRef<Map<string, TerminalHandle>>(new Map());
  const pendingCreateRef = useRef<PaneKey>("main");

  const adoptAgentTerminal = useCallback((id: string) => {
    setAgentTerminalId(id);
  }, []);

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
    setAgentTerminalId(null);
    handlesRef.current.clear();
  }, []);

  useImperativeHandle(
    ref,
    () => ({
      dispatch,
      createTerminal: () => requestNewTerminal("main"),
      reset,
      adoptAgentTerminal,
    }),
    [dispatch, requestNewTerminal, reset, adoptAgentTerminal],
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
              {(["shell", "chat", "agent"] as const).map((m) => (
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
            // Deliberately not gated on folderOpen — a real terminal is
            // useful before any project is open (clone something, look
            // around), and internal/companion's own createTerminal now
            // falls back to the real user home directory when no folder
            // is open rather than refusing outright.
            terminals.length === 0 ? (
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
          ) : mode === "chat" ? (
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
                    <div key={entry.id} className="mb-[5px]">
                      <div className="flex items-start gap-2">
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
                      {entry.unaddressed && (
                        <div className="mt-0.5 pl-[42px]">
                          <Tooltip
                            label="This room has more than one agent — @mention one to have it reply"
                            side="top"
                            wrap
                          >
                            <span className="text-[11px] text-[var(--ide-text-muted)]">
                              Addressed no one
                            </span>
                          </Tooltip>
                        </div>
                      )}
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
          ) : !agentSession ? (
            <AgentStartFormPanel
              agents={agentAgents}
              onStart={onStartAgentSession}
              error={agentStartError}
              disabled={!folderOpen}
            />
          ) : (
            <div className="flex min-h-0 flex-1 flex-col">
              <AgentStatusBar session={agentSession} onStop={onStopAgentSession} />
              <div className="min-h-0 flex-1">
                {agentTerminalId ? (
                  <Terminal
                    ref={(handle) => {
                      if (handle) handlesRef.current.set(agentTerminalId, handle);
                      else handlesRef.current.delete(agentTerminalId);
                    }}
                    onData={(data) => send({ type: "terminal_input", terminal_id: agentTerminalId, data })}
                    onResize={(cols, rows) =>
                      send({ type: "terminal_resize", terminal_id: agentTerminalId, cols, rows })
                    }
                  />
                ) : (
                  <div className="flex h-full items-center justify-center">
                    <p className="text-[12.5px] text-[var(--ide-text-muted)]">Opening this session&apos;s terminal…</p>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
});
