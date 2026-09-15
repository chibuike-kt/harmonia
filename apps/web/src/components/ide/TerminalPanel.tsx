"use client";

import { useEffect, useRef } from "react";
import { OrbMark } from "@/components/OrbMark";
import { PROVIDER_TAG_COLORS, DEFAULT_TAG_COLOR } from "@/components/providerLogos";

export type TranscriptEntry =
  | {
      kind: "chat";
      id: string;
      sender: "human" | "agent";
      agentName?: string;
      provider?: string;
      content: string;
    }
  | {
      kind: "command";
      id: string;
      actor: "human" | "agent";
      agentName?: string;
      provider?: string;
      text: string;
    }
  | { kind: "output"; id: string; text: string }
  | { kind: "error"; id: string; text: string };

export type BottomTab = "problems" | "output" | "terminal";
export type TerminalMode = "shell" | "chat";

function AgentTag({ name, provider }: { name: string; provider?: string }) {
  const color = provider
    ? (PROVIDER_TAG_COLORS[provider] ?? DEFAULT_TAG_COLOR)
    : DEFAULT_TAG_COLOR;
  return (
    <span
      className="flex h-[18px] shrink-0 items-center gap-1 rounded-[5px] border px-1.5 text-[10px]"
      style={{
        background: color.bg,
        color: color.fg,
        borderColor: color.border,
      }}
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

/**
 * The bottom panel's real content — Problems/Output/Terminal tabs, and
 * (on the Terminal tab) a genuine mixed transcript: real chat messages
 * from this IDE session's paired room interleaved with real executed
 * shell commands, visually distinct always (design overhaul point 11).
 * A `➜` prompt only ever appears next to something that actually ran;
 * plain text next to a tag is narration — never rendered the same way.
 *
 * The panel's own open/closed collapse (height, restore strip) is owned
 * by the parent page, not here — this component only renders what's
 * inside it while open.
 */
export function TerminalPanel({
  bottomTab,
  onBottomTabChange,
  onClose,
  onNewTerminal,
  transcript,
  mode,
  onModeChange,
  chatValue,
  onChatChange,
  onChatSubmit,
  shellValue,
  onShellChange,
  onShellSubmit,
  shellRunning,
  onStartShell,
}: {
  bottomTab: BottomTab;
  onBottomTabChange: (tab: BottomTab) => void;
  onClose: () => void;
  onNewTerminal: () => void;
  transcript: TranscriptEntry[];
  mode: TerminalMode;
  onModeChange: (mode: TerminalMode) => void;
  chatValue: string;
  onChatChange: (value: string) => void;
  onChatSubmit: () => void;
  shellValue: string;
  onShellChange: (value: string) => void;
  onShellSubmit: () => void;
  shellRunning: boolean;
  onStartShell: () => void;
}) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    bodyRef.current?.scrollTo({ top: bodyRef.current.scrollHeight });
  }, [transcript]);

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
          <button
            type="button"
            title="New terminal"
            onClick={onNewTerminal}
            className="flex h-6 w-6 items-center justify-center rounded-md text-[var(--ide-text-muted)] hover:bg-[var(--ide-surface-2)] hover:text-[var(--ide-text)]"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
            >
              <path d="M8 2v12M2 8h12" />
            </svg>
          </button>
          <button
            type="button"
            title="Close panel (Ctrl+`)"
            onClick={onClose}
            className="flex h-6 w-6 items-center justify-center rounded-md text-[var(--ide-text-muted)] hover:bg-[var(--ide-surface-2)] hover:text-[var(--ide-text)]"
          >
            <svg
              width="13"
              height="13"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
            >
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </div>
      </div>

      <div className="min-h-0 flex-1 bg-[rgba(26,31,41,.4)]">
        <div
          hidden={bottomTab !== "problems"}
          className="h-full overflow-y-auto px-4 py-3"
        >
          <p className="text-[12.5px] text-[var(--ide-text-muted)]">
            Nothing to show yet.
          </p>
        </div>
        <div
          hidden={bottomTab !== "output"}
          className="h-full overflow-y-auto px-4 py-3"
        >
          <p className="text-[12.5px] text-[var(--ide-text-muted)]">
            Nothing to show yet.
          </p>
        </div>
        <div
          hidden={bottomTab !== "terminal"}
          className="flex h-full flex-col"
        >
          <div
            ref={bodyRef}
            className="no-scrollbar min-h-0 flex-1 overflow-y-auto px-3.5 py-2.5 font-[family-name:var(--login-font-mono)] text-[12.5px]"
          >
            {transcript.length === 0 && (
              <p className="text-[var(--ide-text-muted)]">
                {shellRunning
                  ? "Shell ready."
                  : "No activity yet — start a shell or send a message below."}
              </p>
            )}
            {transcript.map((entry) => {
              if (entry.kind === "chat") {
                return (
                  <div
                    key={entry.id}
                    className="mb-[5px] flex items-start gap-2"
                  >
                    {entry.sender === "human" ? (
                      <HumanTag />
                    ) : (
                      <AgentTag
                        name={entry.agentName ?? "agent"}
                        provider={entry.provider}
                      />
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
                );
              }
              if (entry.kind === "command") {
                return (
                  <div
                    key={entry.id}
                    className="mb-[5px] flex items-start gap-2"
                  >
                    <span className="shrink-0 text-[var(--login-accent)]">
                      ➜
                    </span>
                    {entry.actor === "agent" && (
                      <AgentTag
                        name={entry.agentName ?? "agent"}
                        provider={entry.provider}
                      />
                    )}
                    <span className="text-[var(--ide-text)]">
                      {entry.text}
                    </span>
                  </div>
                );
              }
              if (entry.kind === "error") {
                return (
                  <div
                    key={entry.id}
                    className="mb-[5px] pl-5 whitespace-pre-wrap text-[var(--room-warn)]"
                  >
                    {entry.text}
                  </div>
                );
              }
              return (
                <div
                  key={entry.id}
                  className="mb-[5px] pl-5 whitespace-pre-wrap text-[var(--ide-text-muted)]"
                >
                  {entry.text}
                </div>
              );
            })}
          </div>

          {/* Real, explicit mode switch — Shell sends real commands to
              the companion's own real shell; Chat sends a real message
              through this session's paired room, same pipeline Rooms
              itself uses. Kept as a deliberate toggle rather than
              guessing intent from typed text (a bare word like "ls" is
              genuinely ambiguous between the two). */}
          <div className="flex shrink-0 items-center gap-2 border-t border-[var(--ide-border)] px-3 py-2">
            <div className="flex rounded-md border border-[var(--ide-border-strong)] p-0.5">
              {(["shell", "chat"] as const).map((m) => (
                <button
                  key={m}
                  type="button"
                  onClick={() => {
                    onModeChange(m);
                    inputRef.current?.focus();
                  }}
                  className={`rounded px-2 py-[3px] text-[11px] capitalize ${
                    mode === m
                      ? "bg-[var(--ide-surface-2)] text-[var(--ide-text)]"
                      : "text-[var(--ide-text-muted)]"
                  }`}
                >
                  {m}
                </button>
              ))}
            </div>
            {mode === "shell" && !shellRunning ? (
              <button
                type="button"
                onClick={onStartShell}
                className="rounded-md border border-[var(--ide-border-strong)] px-2.5 py-1 text-[11.5px] text-[var(--ide-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--ide-text)]"
              >
                Start shell
              </button>
            ) : (
              <>
                {mode === "shell" && (
                  <span className="text-[var(--login-accent)]">➜</span>
                )}
                <input
                  ref={inputRef}
                  value={mode === "shell" ? shellValue : chatValue}
                  onChange={(e) =>
                    mode === "shell"
                      ? onShellChange(e.target.value)
                      : onChatChange(e.target.value)
                  }
                  onKeyDown={(e) => {
                    if (e.key !== "Enter") return;
                    e.preventDefault();
                    if (mode === "shell") onShellSubmit();
                    else onChatSubmit();
                  }}
                  placeholder={
                    mode === "shell"
                      ? "Run a real command…"
                      : "Message the room — @Name to address a specific agent…"
                  }
                  className="flex-1 bg-transparent font-[family-name:var(--login-font-mono)] text-[12.5px] text-[var(--ide-text)] outline-none placeholder:text-[var(--ide-text-muted)]"
                />
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
