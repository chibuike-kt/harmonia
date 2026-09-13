"use client";

import { useEffect, useRef, useState } from "react";
import { AddAgentMenu, type AddedAgent } from "./AddAgentMenu";
import { CloseIcon } from "./icons";
import { AgentAvatarGlyph } from "./providerLogos";
import { Tooltip } from "./Tooltip";

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
  onEditObjective: (objective: string) => void;
  agents: RoomAgentSummary[];
  decisions: Decision[];
  roomId: string;
  onAgentAdded: (agent: AddedAgent) => void;
  agentCascadingEnabled: boolean;
  onToggleCascading: (enabled: boolean) => void;
  autonomousPickupEnabled: boolean;
  onTogglePickup: (enabled: boolean) => void;
}

/**
 * Room info side panel — docs/design/room-mockup.html's info-panel,
 * scoped exactly to what the build brief calls buildable now:
 *
 * - Objective: a real, separately captured field (rooms.objective) —
 *   generated asynchronously from the room's early messages once
 *   there's enough content, same auto-generation/manual-override pattern
 *   as the room's own name (see internal/message.ObjectiveGenerator).
 *   Clicking it edits in place, same PATCH-on-commit convention as the
 *   toggles below; once a human edits it, auto-generation never
 *   overwrites it again.
 * - Agents: the room's registered agents, reusing the same
 *   GET /v1/rooms/{room_id}/agents data the composer's @-picker uses.
 * - Decisions: manually pinned messages only — no AI-driven extraction,
 *   see MessageRow's own "Pin as decision" hover action.
 */
export function RoomInfoPanel({
  open,
  onClose,
  objective,
  onEditObjective,
  agents,
  decisions,
  roomId,
  onAgentAdded,
  agentCascadingEnabled,
  onToggleCascading,
  autonomousPickupEnabled,
  onTogglePickup,
}: RoomInfoPanelProps) {
  const [editingObjective, setEditingObjective] = useState(false);
  // Mirrors Sidebar's own rename-input convention: Escape must cancel
  // without the blur it triggers also committing whatever's currently in
  // the field.
  const skipBlurCommitRef = useRef(false);

  // Drives the mobile-only fade/slide-in below — this panel is always
  // mounted (open/closed is just a class swap, not a mount/unmount), so
  // a plain on-mount effect would only ever fire once, not every time
  // `open` flips true. Resetting to false on close and flipping to true
  // on the next animation frame after opening is what gives the browser
  // an actual "before" frame to transition from, rather than the panel
  // just appearing already fully faded in.
  const [entered, setEntered] = useState(false);
  useEffect(() => {
    if (!open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setEntered(false);
      return;
    }
    const id = requestAnimationFrame(() => setEntered(true));
    return () => cancelAnimationFrame(id);
  }, [open]);

  const commitObjective = (raw: string) => {
    setEditingObjective(false);
    const trimmed = raw.trim();
    if (!trimmed || trimmed === objective) return;
    onEditObjective(trimmed);
  };

  return (
    <div
      className={
        open
          ? // Full-screen overlay below md — replacing the timeline, not
            // sharing width with it, same reasoning ArtifactPanel's own
            // mobile treatment documents. md and up reverts to a
            // detached, floating card (m-3 gap on every side, its own
            // rounded corners/shadow) rather than a flush divider panel
            // sharing the timeline's own edges — same treatment as
            // ArtifactPanel's own desktop state. h-[calc(100%-1.5rem)]
            // is h-full minus m-3's 0.75rem top/bottom margins. The
            // opacity/translate pair (neutralized at md and up, where
            // the width transition already does the work) is what
            // fades/slides the mobile overlay in instead of it just
            // snapping into place.
            `fixed inset-0 z-[60] flex h-screen w-screen flex-col bg-[var(--login-surface)] transition-[opacity,transform] duration-200 ease-out md:relative md:inset-auto md:z-auto md:m-3 md:h-[calc(100%-1.5rem)] md:w-[320px] md:shrink-0 md:translate-y-0 md:overflow-hidden md:rounded-2xl md:border md:border-[var(--login-border)] md:opacity-100 md:shadow-[0_12px_40px_rgba(0,0,0,0.45)] md:transition-[width] md:duration-150 ${
              entered ? "translate-y-0 opacity-100" : "translate-y-2 opacity-0"
            }`
          : "h-screen w-0 shrink-0 overflow-hidden transition-[width] duration-150"
      }
    >
      {open && (
        <>
          <div className="flex shrink-0 items-center justify-between border-b border-[var(--login-border)] px-4 py-3.5">
            <span className="text-[14px] font-semibold text-[var(--login-text)]">
              Room info
            </span>
            <Tooltip label="Close">
              <button
                type="button"
                onClick={onClose}
                className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
              >
                <CloseIcon />
              </button>
            </Tooltip>
          </div>

          <div className="no-scrollbar flex-1 overflow-auto p-4">
            <div className="mb-5">
              <div className="mb-2 font-[family-name:var(--login-font-mono)] text-[11px] uppercase tracking-wide text-[var(--login-text-muted)]">
                Objective
              </div>
              {editingObjective ? (
                <textarea
                  autoFocus
                  defaultValue={objective ?? ""}
                  onFocus={(e) => e.currentTarget.select()}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && !e.shiftKey) {
                      e.preventDefault();
                      e.currentTarget.blur();
                    }
                    if (e.key === "Escape") {
                      skipBlurCommitRef.current = true;
                      setEditingObjective(false);
                    }
                  }}
                  onBlur={(e) => {
                    if (skipBlurCommitRef.current) {
                      skipBlurCommitRef.current = false;
                      return;
                    }
                    commitObjective(e.currentTarget.value);
                  }}
                  rows={3}
                  className="w-full resize-none rounded-lg border border-[var(--login-accent)] bg-transparent p-2 text-[13.5px] leading-[1.5] text-[var(--login-text)] outline-none"
                />
              ) : (
                <p
                  onClick={() => setEditingObjective(true)}
                  className="-mx-2 cursor-text rounded-lg px-2 py-1.5 text-[13.5px] leading-[1.5] text-[var(--login-text)] hover:bg-[var(--login-surface-2)]"
                >
                  {objective ||
                    "Not enough conversation yet to summarize an objective — click to write one."}
                </p>
              )}
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
                      <AgentAvatarGlyph
                        provider={a.provider}
                        name={a.name}
                        size={12}
                      />
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

            <div className="mb-5">
              <div className="mb-2 font-[family-name:var(--login-font-mono)] text-[11px] uppercase tracking-wide text-[var(--login-text-muted)]">
                Agent cascading
              </div>
              <button
                type="button"
                role="switch"
                aria-checked={agentCascadingEnabled}
                onClick={() => onToggleCascading(!agentCascadingEnabled)}
                className="flex w-full items-center justify-between gap-3 rounded-lg border border-[var(--login-border-strong)] px-3 py-2 text-left hover:border-[var(--login-accent)]"
              >
                <span className="text-[13px] leading-[1.4] text-[var(--login-text)]">
                  Let agents @mention each other in this room, capped at a few
                  hops. Off by default.
                </span>
                <span
                  className={`relative h-5 w-9 shrink-0 rounded-full transition-colors ${
                    agentCascadingEnabled
                      ? "bg-[var(--login-accent)]"
                      : "bg-[var(--login-border-strong)]"
                  }`}
                >
                  <span
                    className={`absolute top-0.5 h-4 w-4 rounded-full bg-[var(--login-bg)] transition-transform ${
                      agentCascadingEnabled
                        ? "translate-x-[18px]"
                        : "translate-x-0.5"
                    }`}
                  />
                </span>
              </button>
            </div>

            <div className="mb-5">
              <div className="mb-2 font-[family-name:var(--login-font-mono)] text-[11px] uppercase tracking-wide text-[var(--login-text-muted)]">
                Autonomous pickup
              </div>
              <button
                type="button"
                role="switch"
                aria-checked={autonomousPickupEnabled}
                onClick={() => onTogglePickup(!autonomousPickupEnabled)}
                className="flex w-full items-center justify-between gap-3 rounded-lg border border-[var(--login-border-strong)] px-3 py-2 text-left hover:border-[var(--login-accent)]"
              >
                <span className="text-[13px] leading-[1.4] text-[var(--login-text)]">
                  Let every agent evaluate unaddressed messages and pick up ones
                  that need a reply. Real spend per evaluation. Off by default.
                </span>
                <span
                  className={`relative h-5 w-9 shrink-0 rounded-full transition-colors ${
                    autonomousPickupEnabled
                      ? "bg-[var(--login-accent)]"
                      : "bg-[var(--login-border-strong)]"
                  }`}
                >
                  <span
                    className={`absolute top-0.5 h-4 w-4 rounded-full bg-[var(--login-bg)] transition-transform ${
                      autonomousPickupEnabled
                        ? "translate-x-[18px]"
                        : "translate-x-0.5"
                    }`}
                  />
                </span>
              </button>
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
