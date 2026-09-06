"use client";

import { AddAgentMenu, type AddedAgent } from "./AddAgentMenu";
import { CloseIcon } from "./icons";
import { AgentAvatarGlyph } from "./providerLogos";

export interface RoomAgentSummary {
  id: string;
  name: string;
  provider: string;
  capabilities: string[];
}

export interface Decision {
  id: string;
  message_id: string;
  content: string;
  created_at: string;
}

interface RoomInfoPanelProps {
  open: boolean;
  onClose: () => void;
  objective: string | null;
  agents: RoomAgentSummary[];
  decisions: Decision[];
  roomId: string;
  onAgentAdded: (agent: AddedAgent) => void;
}

/**
 * Room info side panel — docs/design/room-mockup.html's info-panel,
 * scoped exactly to what the build brief calls buildable now:
 *
 * - Objective: the room's first message content, not a separately
 *   captured field — there's no real capture mechanism for an actual
 *   objective yet, so this is a stand-in, not a claim that a "real"
 *   objective exists somewhere.
 * - Agents: the room's registered agents, reusing the same
 *   GET /v1/rooms/{room_id}/agents data the composer's @-picker uses.
 * - Decisions: manually pinned messages only — no AI-driven extraction,
 *   see MessageRow's own "Pin as decision" hover action.
 */
export function RoomInfoPanel({
  open,
  onClose,
  objective,
  agents,
  decisions,
  roomId,
  onAgentAdded,
}: RoomInfoPanelProps) {
  return (
    <div
      className={
        open
          ? "flex h-screen w-[320px] shrink-0 flex-col border-l border-[var(--login-border)] bg-[var(--login-surface)] transition-[width] duration-150"
          : "h-screen w-0 shrink-0 overflow-hidden transition-[width] duration-150"
      }
    >
      {open && (
        <>
          <div className="flex shrink-0 items-center justify-between border-b border-[var(--login-border)] px-4 py-3.5">
            <span className="text-[14px] font-semibold text-[var(--login-text)]">
              Room info
            </span>
            <button
              type="button"
              title="Close"
              onClick={onClose}
              className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
            >
              <CloseIcon />
            </button>
          </div>

          <div className="no-scrollbar flex-1 overflow-auto p-4">
            <div className="mb-5">
              <div className="mb-2 font-[family-name:var(--login-font-mono)] text-[11px] uppercase tracking-wide text-[var(--login-text-muted)]">
                Objective
              </div>
              <p className="text-[13.5px] leading-[1.5] text-[var(--login-text)]">
                {objective || "No messages yet in this room."}
              </p>
            </div>

            <div className="mb-5">
              <div className="mb-2 flex items-center justify-between">
                <span className="font-[family-name:var(--login-font-mono)] text-[11px] uppercase tracking-wide text-[var(--login-text-muted)]">
                  Agents
                </span>
                <AddAgentMenu
                  roomId={roomId}
                  onAdded={onAgentAdded}
                  label="Add agent"
                />
              </div>
              {agents.length === 0 ? (
                <p className="text-[13.5px] text-[var(--login-text-muted)]">
                  No agents connected to this room.
                </p>
              ) : (
                agents.map((a) => (
                  <div
                    key={a.id}
                    className="flex items-center gap-2 py-1.5 text-[13.5px] text-[var(--login-text)]"
                  >
                    <span className="flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] text-[9.5px] font-semibold">
                      <AgentAvatarGlyph provider={a.provider} name={a.name} size={12} />
                    </span>
                    {a.name}
                    {a.capabilities.length > 0 && (
                      <span className="text-[var(--login-text-muted)]">
                        — {a.capabilities.join(", ")}
                      </span>
                    )}
                  </div>
                ))
              )}
            </div>

            <div>
              <div className="mb-2 font-[family-name:var(--login-font-mono)] text-[11px] uppercase tracking-wide text-[var(--login-text-muted)]">
                Decisions
              </div>
              {decisions.length === 0 ? (
                <p className="text-[13.5px] text-[var(--login-text-muted)]">
                  Nothing pinned yet — hover a message and pin it to surface it
                  here.
                </p>
              ) : (
                decisions.map((d) => (
                  <div
                    key={d.id}
                    className="border-b border-[var(--login-border)] py-2 text-[13px] leading-[1.5] text-[var(--login-text)] last:border-b-0"
                  >
                    {d.content}
                  </div>
                ))
              )}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
