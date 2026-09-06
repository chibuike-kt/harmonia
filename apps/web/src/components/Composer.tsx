"use client";

import { useEffect, useRef, useState } from "react";
import { FileIcon, PlusIcon, SearchIcon, SendIcon } from "./icons";
import { AgentAvatarGlyph } from "./providerLogos";

export interface RoomAgent {
  id: string;
  name: string;
  provider: string;
}

interface ComposerProps {
  agents: RoomAgent[];
  disabled?: boolean;
  onSend: (content: string, mentionedAgentId: string | null) => void;
}

const MAX_HEIGHT = 160;

/**
 * Message composer — text input, send, an @-picker for the room's own
 * agents (sends the structured mentioned_agent_id the backend expects,
 * never text-parsed), and a "+" menu with two honestly-labeled,
 * currently-inert items. See ADR-004's addendum: web search and file
 * attachment are real, near-term follow-up work once this phase ships,
 * not a permanent dead end — labeled "soon," not left mysteriously
 * empty or given vague "coming soon" copy that hides what's actually
 * planned.
 */
export function Composer({ agents, disabled, onSend }: ComposerProps) {
  const [value, setValue] = useState("");
  const [plusOpen, setPlusOpen] = useState(false);
  const [mentionedAgent, setMentionedAgent] = useState<RoomAgent | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, MAX_HEIGHT) + "px";
  }, [value]);

  useEffect(() => {
    function onDocumentClick(event: MouseEvent) {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setPlusOpen(false);
      }
    }
    document.addEventListener("click", onDocumentClick);
    return () => document.removeEventListener("click", onDocumentClick);
  }, []);

  const handleSend = () => {
    const trimmed = value.trim();
    if (!trimmed || disabled) return;
    onSend(trimmed, mentionedAgent?.id ?? null);
    setValue("");
    setMentionedAgent(null);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <div className="flex justify-center px-6 pb-5 pt-3.5">
      <div
        ref={containerRef}
        className="relative w-full max-w-[720px] rounded-[14px] border border-[var(--login-border-strong)] bg-[var(--login-surface)] p-2.5 pl-3.5"
      >
        {plusOpen && (
          <div className="absolute bottom-[calc(100%+8px)] left-2.5 flex min-w-[200px] flex-col gap-px rounded-[10px] border border-[var(--login-border-strong)] bg-[var(--login-surface)] p-1.5 shadow-[0_8px_24px_rgba(0,0,0,0.4)]">
            <div className="flex cursor-not-allowed items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13.5px] text-[var(--login-text-secondary)] opacity-55">
              <SearchIcon />
              Search the web
              <span className="ml-auto font-[family-name:var(--login-font-mono)] text-[10.5px] text-[var(--login-text-muted)]">
                soon
              </span>
            </div>
            <div className="flex cursor-not-allowed items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13.5px] text-[var(--login-text-secondary)] opacity-55">
              <FileIcon />
              Add a file
              <span className="ml-auto font-[family-name:var(--login-font-mono)] text-[10.5px] text-[var(--login-text-muted)]">
                soon
              </span>
            </div>
          </div>
        )}

        {mentionedAgent && (
          <div className="mb-1.5 flex items-center gap-1.5 px-1">
            <span className="flex items-center gap-1.5 rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] py-0.5 pl-1 pr-2 text-[12px] text-[var(--login-text-secondary)]">
              <span className="flex h-4 w-4 items-center justify-center rounded-full bg-[var(--login-border-strong)] text-[8px] font-semibold text-[var(--login-accent)]">
                <AgentAvatarGlyph
                  provider={mentionedAgent.provider}
                  name={mentionedAgent.name}
                  size={9}
                />
              </span>
              @{mentionedAgent.name}
              <button
                type="button"
                aria-label="Remove mention"
                onClick={() => setMentionedAgent(null)}
                className="ml-0.5 text-[var(--login-text-muted)] hover:text-[var(--login-text)]"
              >
                ×
              </button>
            </span>
          </div>
        )}

        {!mentionedAgent && agents.length > 0 && (
          <div className="mb-1.5 flex flex-wrap items-center gap-1.5 px-1">
            <span className="font-[family-name:var(--login-font-mono)] text-[11px] text-[var(--login-text-muted)]">
              Address:
            </span>
            {agents.map((a) => (
              <button
                key={a.id}
                type="button"
                onClick={() => setMentionedAgent(a)}
                className="flex items-center gap-1.5 rounded-full border border-[var(--login-border-strong)] py-1 pl-1.5 pr-2.5 text-[12px] text-[var(--login-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--login-text)]"
              >
                <span className="flex h-4 w-4 items-center justify-center rounded-full bg-[var(--login-border-strong)] text-[8px] font-semibold text-[var(--login-accent)]">
                  <AgentAvatarGlyph provider={a.provider} name={a.name} size={9} />
                </span>
                @{a.name}
              </button>
            ))}
          </div>
        )}

        <div className="flex items-end gap-2.5">
          <button
            type="button"
            aria-label="More options"
            onClick={() => setPlusOpen((o) => !o)}
            className="flex h-[34px] w-[34px] shrink-0 items-center justify-center rounded-lg text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)]"
          >
            <PlusIcon size={17} strokeWidth={1.6} />
          </button>
          <textarea
            ref={textareaRef}
            rows={1}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Message this room — @mention an agent to address it"
            className="no-scrollbar max-h-[160px] flex-1 resize-none bg-transparent py-2 font-[family-name:var(--login-font-sans)] text-[14.5px] leading-[1.5] text-[var(--login-text)] outline-none placeholder:text-[var(--login-text-muted)]"
          />
          <button
            type="button"
            aria-label="Send message"
            disabled={disabled || value.trim() === ""}
            onClick={handleSend}
            className="flex h-[34px] w-[34px] shrink-0 items-center justify-center rounded-lg bg-[var(--login-accent)] text-[var(--login-bg)] hover:bg-[#63e0d1] disabled:cursor-not-allowed disabled:opacity-40"
          >
            <SendIcon />
          </button>
        </div>
      </div>
    </div>
  );
}
